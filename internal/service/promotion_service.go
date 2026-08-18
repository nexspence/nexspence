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
// An empty PathFilter matches everything; a filter that fails to compile or
// evaluate matches nothing (the enforcement path reports that distinctly).
func (s *PromotionService) matchesPathFilter(rule domain.PromotionRule, comp *domain.Component) bool {
	matched, err := s.evalPathFilter(rule, comp)
	return err == nil && matched
}

// evalPathFilter evaluates the rule's path filter against the component,
// separating "did not match" from "the filter itself is broken" — a rule
// whose filter errors must fail closed, but with an error that sends the
// operator to the filter, not the component.
func (s *PromotionService) evalPathFilter(rule domain.PromotionRule, comp *domain.Component) (bool, error) {
	if rule.PathFilter == "" {
		return true, nil
	}
	ast, issues := s.celEnv.Compile(rule.PathFilter)
	if issues != nil && issues.Err() != nil {
		return false, fmt.Errorf("path filter does not compile: %w", issues.Err())
	}
	prg, err := s.celEnv.Program(ast)
	if err != nil {
		return false, fmt.Errorf("path filter does not compile: %w", err)
	}
	path := "/" + comp.Group + "/" + comp.Name
	vars := map[string]any{
		"format":     comp.Format,
		"path":       path,
		"repository": comp.Repository,
	}
	out, _, err := prg.Eval(vars)
	if err != nil {
		return false, fmt.Errorf("path filter failed to evaluate: %w", err)
	}
	matched, ok := out.Value().(bool)
	if !ok {
		return false, fmt.Errorf("path filter evaluates to %T, not a boolean", out.Value())
	}
	return matched, nil
}

// ruleAppliesTo reports whether the rule actually covers the component: the
// component lives in the rule's from_repo and passes its path filter. This is
// the same test ListRulesForComponent offers rules by — but offering is not
// enforcing: Promote takes a caller-supplied (rule_id, component_id) pair, so
// without this check any caller with promotion permission could promote an
// arbitrary component with whichever rule has the laxest gates, bypassing a
// stricter rule's require_scan_pass/require_manual_approval on the pair that
// actually covers it (#255).
func (s *PromotionService) ruleAppliesTo(rule *domain.PromotionRule, comp *domain.Component) error {
	if comp.Repository != rule.FromRepo {
		return fmt.Errorf("component %s lives in repository %q, which rule %q does not promote from (%q)",
			comp.ID, comp.Repository, rule.Name, rule.FromRepo)
	}
	matched, err := s.evalPathFilter(*rule, comp)
	if err != nil {
		// Fail closed, but honestly: a broken filter is the rule's problem,
		// and "does not match" would send the operator to the wrong place.
		return fmt.Errorf("rule %q: %w", rule.Name, err)
	}
	if !matched {
		return fmt.Errorf("component %s does not match rule %q's path filter", comp.ID, rule.Name)
	}
	return nil
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
		ast, issues := s.celEnv.Compile(rule.PathFilter)
		if issues != nil && issues.Err() != nil {
			return fmt.Errorf("invalid path_filter CEL expression: %w", issues.Err())
		}
		// A filter that compiles to a non-bool (`path`, `"foo"`) would fail
		// every applicability check downstream while looking valid here.
		if ast.OutputType() != cel.BoolType {
			return fmt.Errorf("invalid path_filter CEL expression: must evaluate to a boolean, not %s", ast.OutputType())
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
		ast, issues := s.celEnv.Compile(rule.PathFilter)
		if issues != nil && issues.Err() != nil {
			return fmt.Errorf("invalid path_filter CEL expression: %w", issues.Err())
		}
		if ast.OutputType() != cel.BoolType {
			return fmt.Errorf("invalid path_filter CEL expression: must evaluate to a boolean, not %s", ast.OutputType())
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

// scanGate refuses promotion when the rule demands a clean scan and the
// component's latest scan is missing or dirty. Malicious is checked alongside
// the CVE tiers, not folded into them: a malicious-package report has no CVSS
// level, so a gate reading only Critical/High would pass a compromised
// release with a spotless CVE record straight into production.
func (s *PromotionService) scanGate(ctx context.Context, rule *domain.PromotionRule, compID string) error {
	if !rule.RequireScanPass {
		return nil
	}
	scan, err := s.scanRepo.GetLatestByComponent(ctx, compID)
	if err != nil || scan == nil {
		return fmt.Errorf("component %s: scan required but not yet run", compID)
	}
	if scan.Malicious > 0 || scan.Critical > 0 || scan.High > 0 {
		return fmt.Errorf("component %s: scan has %d malicious, %d critical, %d high findings",
			compID, scan.Malicious, scan.Critical, scan.High)
	}
	return nil
}

// Promote creates promotion requests for each component. Auto-approves when require_manual_approval=false.
func (s *PromotionService) Promote(ctx context.Context, ruleID string, componentIDs []string, requestedByID string) ([]domain.PromotionRequest, error) {
	rule, err := s.promotionRepo.GetRule(ctx, ruleID)
	if err != nil || rule == nil {
		return nil, fmt.Errorf("promotion rule not found: %s", ruleID)
	}

	// The whole batch is validated before anything is created or copied: with
	// an auto-approve rule the loop below executes copies as it goes, so a
	// mid-batch refusal would leave earlier components already promoted while
	// the caller is told the batch failed. Refusing before any request row
	// exists also spares reviewers pending requests nobody could legitimately
	// approve.
	for _, compID := range componentIDs {
		comp, cerr := s.componentRepo.Get(ctx, compID)
		if cerr != nil || comp == nil {
			return nil, fmt.Errorf("component not found: %s", compID)
		}
		if aerr := s.ruleAppliesTo(rule, comp); aerr != nil {
			return nil, aerr
		}
		if serr := s.scanGate(ctx, rule, compID); serr != nil {
			return nil, serr
		}
	}

	var results []domain.PromotionRequest
	for _, compID := range componentIDs {
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
	// Re-checked at copy time, not just when the request was filed: Approve
	// runs later, and the component may have moved, the rule changed, or a
	// scan found something while the request sat pending — the invariants
	// have to hold when the bytes actually move.
	if err := s.ruleAppliesTo(rule, comp); err != nil {
		return err
	}
	if err := s.scanGate(ctx, rule, comp.ID); err != nil {
		return err
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
		Extra:        promotedExtra(comp.Extra),
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
