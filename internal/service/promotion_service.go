package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/cel-go/cel"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/storage"
)

// PromotionService copies artifacts between repositories according to promotion rules.
type PromotionService struct {
	promotionRepo repository.PromotionRepo
	componentRepo repository.ComponentRepo
	assetRepo     repository.AssetRepo
	repoRepo      repository.RepositoryRepo
	blobRepo      repository.BlobStoreRepo
	scanRepo      repository.ScanResultRepo
	blobStore     storage.BlobStore
	blobRegistry  *storage.Registry
	webhooks      domain.WebhookDispatcher

	celEnv *cel.Env
}

func NewPromotionService(
	promotionRepo repository.PromotionRepo,
	componentRepo repository.ComponentRepo,
	assetRepo repository.AssetRepo,
	repoRepo repository.RepositoryRepo,
	blobRepo repository.BlobStoreRepo,
	scanRepo repository.ScanResultRepo,
	blobStore storage.BlobStore,
	blobRegistry *storage.Registry,
) (*PromotionService, error) {
	env, err := cel.NewEnv(
		cel.Variable("format", cel.StringType),
		cel.Variable("path", cel.StringType),
		cel.Variable("repository", cel.StringType),
	)
	if err != nil {
		return nil, fmt.Errorf("promotion cel env: %w", err)
	}
	return &PromotionService{
		promotionRepo: promotionRepo,
		componentRepo: componentRepo,
		assetRepo:     assetRepo,
		repoRepo:      repoRepo,
		blobRepo:      blobRepo,
		scanRepo:      scanRepo,
		blobStore:     blobStore,
		blobRegistry:  blobRegistry,
		celEnv:        env,
	}, nil
}

func (s *PromotionService) WithWebhooks(w domain.WebhookDispatcher) *PromotionService {
	s.webhooks = w
	return s
}

// matchesPathFilter returns true when the component matches the rule's path filter.
// An empty PathFilter matches everything.
func (s *PromotionService) matchesPathFilter(rule domain.PromotionRule, comp *domain.Component) bool {
	if rule.PathFilter == "" {
		return true
	}
	ast, issues := s.celEnv.Compile(rule.PathFilter)
	if issues != nil && issues.Err() != nil {
		return false
	}
	prg, err := s.celEnv.Program(ast)
	if err != nil {
		return false
	}
	path := "/" + comp.Group + "/" + comp.Name
	vars := map[string]any{
		"format":     comp.Format,
		"path":       path,
		"repository": comp.Repository,
	}
	out, _, err := prg.Eval(vars)
	if err != nil {
		return false
	}
	matched, _ := out.Value().(bool)
	return matched
}

func (s *PromotionService) ListRules(ctx context.Context) ([]domain.PromotionRule, error) {
	return s.promotionRepo.ListRules(ctx)
}

func (s *PromotionService) GetRule(ctx context.Context, id string) (*domain.PromotionRule, error) {
	return s.promotionRepo.GetRule(ctx, id)
}

func (s *PromotionService) CreateRule(ctx context.Context, rule *domain.PromotionRule) error {
	if rule.Name == "" {
		return fmt.Errorf("name is required")
	}
	if rule.FromRepo == "" || rule.ToRepo == "" {
		return fmt.Errorf("from_repo and to_repo are required")
	}
	if rule.FromRepo == rule.ToRepo {
		return fmt.Errorf("from_repo and to_repo must be different")
	}
	if rule.PathFilter != "" {
		if _, issues := s.celEnv.Compile(rule.PathFilter); issues != nil && issues.Err() != nil {
			return fmt.Errorf("invalid path_filter CEL expression: %w", issues.Err())
		}
	}
	return s.promotionRepo.CreateRule(ctx, rule)
}

func (s *PromotionService) UpdateRule(ctx context.Context, rule *domain.PromotionRule) error {
	if rule.Name == "" {
		return fmt.Errorf("name is required")
	}
	if rule.FromRepo == rule.ToRepo {
		return fmt.Errorf("from_repo and to_repo must be different")
	}
	if rule.PathFilter != "" {
		if _, issues := s.celEnv.Compile(rule.PathFilter); issues != nil && issues.Err() != nil {
			return fmt.Errorf("invalid path_filter CEL expression: %w", issues.Err())
		}
	}
	return s.promotionRepo.UpdateRule(ctx, rule)
}

func (s *PromotionService) DeleteRule(ctx context.Context, id string) error {
	return s.promotionRepo.DeleteRule(ctx, id)
}

// ListRulesForComponent returns promotion rules that apply to the given component.
func (s *PromotionService) ListRulesForComponent(ctx context.Context, componentID string) ([]domain.PromotionRule, error) {
	comp, err := s.componentRepo.Get(ctx, componentID)
	if err != nil || comp == nil {
		return nil, fmt.Errorf("component not found: %s", componentID)
	}
	rules, err := s.promotionRepo.ListRulesByFromRepo(ctx, comp.Repository)
	if err != nil {
		return nil, err
	}
	var matching []domain.PromotionRule
	for _, r := range rules {
		if s.matchesPathFilter(r, comp) {
			matching = append(matching, r)
		}
	}
	return matching, nil
}

func (s *PromotionService) ListRequests(ctx context.Context, status string) ([]domain.PromotionRequest, error) {
	return s.promotionRepo.ListRequests(ctx, status)
}

// Promote creates promotion requests for each component. Auto-approves when require_manual_approval=false.
func (s *PromotionService) Promote(ctx context.Context, ruleID string, componentIDs []string, requestedByID string) ([]domain.PromotionRequest, error) {
	rule, err := s.promotionRepo.GetRule(ctx, ruleID)
	if err != nil || rule == nil {
		return nil, fmt.Errorf("promotion rule not found: %s", ruleID)
	}

	var results []domain.PromotionRequest
	for _, compID := range componentIDs {
		if rule.RequireScanPass {
			scan, serr := s.scanRepo.GetLatestByComponent(ctx, compID)
			if serr != nil || scan == nil {
				return nil, fmt.Errorf("component %s: scan required but not yet run", compID)
			}
			if scan.Critical > 0 || scan.High > 0 {
				return nil, fmt.Errorf("component %s: scan has %d critical, %d high findings", compID, scan.Critical, scan.High)
			}
		}

		req := &domain.PromotionRequest{
			RuleID:      ruleID,
			ComponentID: compID,
			Status:      domain.PromotionPending,
			RequestedBy: requestedByID,
		}
		if err := s.promotionRepo.CreateRequest(ctx, req); err != nil {
			return nil, fmt.Errorf("create promotion request: %w", err)
		}

		if !rule.RequireManualApproval {
			if copyErr := s.executeCopy(ctx, req, rule); copyErr != nil {
				now := time.Now()
				_ = s.promotionRepo.UpdateRequestStatus(ctx, req.ID, domain.PromotionFailed,
					nil, nil, &now, copyErr.Error())
				req.Status = domain.PromotionFailed
				req.Error = copyErr.Error()
			} else {
				now := time.Now()
				_ = s.promotionRepo.UpdateRequestStatus(ctx, req.ID, domain.PromotionCompleted,
					nil, nil, &now, "")
				req.Status = domain.PromotionCompleted
				req.CompletedAt = &now
			}
		}

		results = append(results, *req)
	}
	return results, nil
}

// Approve approves a pending promotion request and copies the artifact.
func (s *PromotionService) Approve(ctx context.Context, requestID, reviewerID string) error {
	req, err := s.promotionRepo.GetRequest(ctx, requestID)
	if err != nil || req == nil {
		return fmt.Errorf("promotion request not found: %s", requestID)
	}
	if req.Status != domain.PromotionPending {
		return fmt.Errorf("request is not pending (status: %s)", req.Status)
	}
	rule, err := s.promotionRepo.GetRule(ctx, req.RuleID)
	if err != nil || rule == nil {
		return fmt.Errorf("promotion rule not found: %s", req.RuleID)
	}
	now := time.Now()
	if copyErr := s.executeCopy(ctx, req, rule); copyErr != nil {
		_ = s.promotionRepo.UpdateRequestStatus(ctx, req.ID, domain.PromotionFailed,
			&reviewerID, &now, &now, copyErr.Error())
		return copyErr
	}
	return s.promotionRepo.UpdateRequestStatus(ctx, req.ID, domain.PromotionCompleted,
		&reviewerID, &now, &now, "")
}

// Reject rejects a pending promotion request.
func (s *PromotionService) Reject(ctx context.Context, requestID, reviewerID, reason string) error {
	req, err := s.promotionRepo.GetRequest(ctx, requestID)
	if err != nil || req == nil {
		return fmt.Errorf("promotion request not found: %s", requestID)
	}
	if req.Status != domain.PromotionPending {
		return fmt.Errorf("request is not pending (status: %s)", req.Status)
	}
	now := time.Now()
	return s.promotionRepo.UpdateRequestStatus(ctx, req.ID, domain.PromotionRejected,
		&reviewerID, &now, nil, reason)
}

// executeCopy copies a component's blobs and metadata from from_repo to to_repo.
func (s *PromotionService) executeCopy(ctx context.Context, req *domain.PromotionRequest, rule *domain.PromotionRule) error {
	comp, err := s.componentRepo.Get(ctx, req.ComponentID)
	if err != nil || comp == nil {
		return fmt.Errorf("source component not found: %s", req.ComponentID)
	}
	toRepo, err := s.repoRepo.Get(ctx, rule.ToRepo)
	if err != nil || toRepo == nil {
		return fmt.Errorf("target repository not found: %s", rule.ToRepo)
	}

	toStore, toBlobStoreID := s.resolveStore(ctx, toRepo.BlobStoreID)

	assets, err := s.assetRepo.ListByComponentID(ctx, req.ComponentID)
	if err != nil {
		return fmt.Errorf("list assets: %w", err)
	}

	newComp := &domain.Component{
		RepositoryID: toRepo.ID,
		Repository:   toRepo.Name,
		Format:       string(toRepo.Format),
		Group:        comp.Group,
		Name:         comp.Name,
		Version:      comp.Version,
		Tags:         comp.Tags,
	}
	if err := s.componentRepo.Create(ctx, newComp); err != nil {
		return fmt.Errorf("upsert component in target: %w", err)
	}

	for _, asset := range assets {
		blobStoreID := asset.BlobStoreID
		fromStore, _ := s.resolveStore(ctx, &blobStoreID)

		newBlobKey := base.BlobKey(toRepo.Name, asset.Path)

		rc, size, err := fromStore.Get(ctx, asset.BlobKey)
		if err != nil {
			return fmt.Errorf("read blob %s: %w", asset.BlobKey, err)
		}
		if putErr := toStore.Put(ctx, newBlobKey, rc, size); putErr != nil {
			_ = rc.Close()
			return fmt.Errorf("write blob %s: %w", newBlobKey, putErr)
		}
		_ = rc.Close()

		newAsset := &domain.Asset{
			ComponentID:  newComp.ID,
			RepositoryID: toRepo.ID,
			Repository:   toRepo.Name,
			Path:         asset.Path,
			BlobStoreID:  toBlobStoreID,
			BlobKey:      newBlobKey,
			SizeBytes:    size,
			ContentType:  asset.ContentType,
			SHA256:       asset.SHA256,
			SHA1:         asset.SHA1,
			MD5:          asset.MD5,
		}
		if err := s.assetRepo.Create(ctx, newAsset); err != nil {
			return fmt.Errorf("create asset record: %w", err)
		}
	}

	if s.webhooks != nil {
		s.webhooks.Dispatch(domain.WebhookPayload{
			Event:      domain.EventArtifactPublished,
			Timestamp:  time.Now(),
			Repository: toRepo.Name,
			Component: map[string]any{
				"group":   newComp.Group,
				"name":    newComp.Name,
				"version": newComp.Version,
				"format":  string(toRepo.Format),
			},
		})
	}
	return nil
}

// resolveStore returns the physical BlobStore for a given blobStoreID pointer.
func (s *PromotionService) resolveStore(ctx context.Context, blobStoreID *string) (storage.BlobStore, string) {
	if blobStoreID == nil || *blobStoreID == "" {
		return s.blobStore, ""
	}
	bsMeta, err := s.blobRepo.GetByID(ctx, *blobStoreID)
	if err != nil || bsMeta == nil {
		return s.blobStore, ""
	}
	bs, err := s.blobRegistry.Get(ctx, storage.BlobStoreDescriptor{
		ID:     bsMeta.ID,
		Type:   bsMeta.Type,
		Config: bsMeta.Config,
	})
	if err != nil {
		return s.blobStore, ""
	}
	return bs, bsMeta.ID
}
