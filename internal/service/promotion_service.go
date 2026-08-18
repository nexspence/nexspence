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

// NewPromotionService constructs a service for build promotion between repositories,
// initializing the CEL environment used to evaluate rule path filters.
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

// WithWebhooks attaches a dispatcher for promotion lifecycle events and returns s.
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

// ListRules returns all promotion rules.
func (s *PromotionService) ListRules(ctx context.Context) ([]domain.PromotionRule, error) {
	return s.promotionRepo.ListRules(ctx)
}

// GetRule returns the promotion rule with the given id.
func (s *PromotionService) GetRule(ctx context.Context, id string) (*domain.PromotionRule, error) {
	return s.promotionRepo.GetRule(ctx, id)
}

// CreateRule validates and persists a new promotion rule.
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

// UpdateRule validates and persists changes to an existing promotion rule.
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

// DeleteRule removes the promotion rule with the given id.
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

// ListRequests returns promotion requests, optionally filtered by status.
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
			// Malicious is checked alongside the CVE tiers, not folded into
			// them: a malicious-package report has no CVSS level, so a gate
			// reading only Critical/High would pass a compromised release with
			// a spotless CVE record straight into production.
			if scan.Malicious > 0 || scan.Critical > 0 || scan.High > 0 {
				return nil, fmt.Errorf("component %s: scan has %d malicious, %d critical, %d high findings",
					compID, scan.Malicious, scan.Critical, scan.High)
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

	toStore, toBlobStoreID, err := s.resolveStore(ctx, toRepo.BlobStoreID)
	if err != nil {
		return fmt.Errorf("target %s: %w", toRepo.Name, err)
	}

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
		Extra:        promotedExtra(comp.Extra),
	}
	if err := s.componentRepo.Create(ctx, newComp); err != nil {
		return fmt.Errorf("upsert component in target: %w", err)
	}

	for _, asset := range assets {
		blobStoreID := asset.BlobStoreID
		fromStore, _, err := s.resolveStore(ctx, &blobStoreID)
		if err != nil {
			return fmt.Errorf("source asset %s: %w", asset.Path, err)
		}

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

// promotedExtra is the source component's metadata as the promoted copy should
// carry it. The keys describe the content, and the copy is that content byte for
// byte, so they stay true in the target repository. The OCI ones have to travel
// in particular: a signature manifest is found through Extra["oci_subject"], so
// a copy without it is a signature the target repository's referrers API can
// never list.
//
// scan_result is the one key left behind. It records a scan run against the
// SOURCE repository's image reference, and the scan rows the promotion gate
// actually reads are keyed by component ID and are not copied either — so
// carrying it would report a scan of this copy that was never run.
func promotedExtra(extra map[string]any) map[string]any {
	if len(extra) == 0 {
		return nil
	}
	out := make(map[string]any, len(extra))
	for k, v := range extra {
		if k == "scan_result" {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// resolveStore returns the physical BlobStore for a given blobStoreID pointer,
// and the id to record on the copied assets.
//
// The id is never empty: assets.blob_store_id is a NOT NULL foreign key, so
// the "use the default store" case (a repository with no explicit
// blobStoreID — the common one) must resolve to the real seeded row's UUID,
// the same lookup resolveBlobStoreRef performs for ordinary uploads. The old
// empty-string answer made every promotion touching a default-store
// repository fail with a raw constraint error (#256).
func (s *PromotionService) resolveStore(ctx context.Context, blobStoreID *string) (storage.BlobStore, string, error) {
	if blobStoreID == nil || *blobStoreID == "" {
		// The physical store is the process's configured default (s.blobStore,
		// exactly what the old code used); only the id must come from the
		// seeded "default" row — migration 001 always creates it.
		bsMeta, err := s.blobRepo.Get(ctx, "default")
		if err != nil {
			return nil, "", fmt.Errorf("blob store: %w", err)
		}
		if bsMeta == nil {
			return nil, "", fmt.Errorf("default blob store not found (seed blob_stores or assign repository.blobStoreId)")
		}
		return s.blobStore, bsMeta.ID, nil
	}
	bsMeta, err := s.blobRepo.GetByID(ctx, *blobStoreID)
	if err != nil {
		return nil, "", fmt.Errorf("blob store: %w", err)
	}
	if bsMeta == nil {
		return nil, "", fmt.Errorf("blob store id %q not found", *blobStoreID)
	}
	bs, err := s.blobRegistry.Get(ctx, storage.BlobStoreDescriptor{
		ID:     bsMeta.ID,
		Type:   bsMeta.Type,
		Config: bsMeta.Config,
	})
	if err != nil {
		return nil, "", fmt.Errorf("blob store %q: %w", bsMeta.Name, err)
	}
	return bs, bsMeta.ID, nil
}
