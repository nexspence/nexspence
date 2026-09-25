package service

import (
	"context"
	"errors"
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
	blobResolver  StoreResolver
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
	blobResolver StoreResolver,
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
		blobResolver:  blobResolver,
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

// ErrAmbiguousPromotionRule reports that more than one promotion rule covers the
// same component for the same repository pair. Callers map it to 409: it is not
// a malformed request, it is a configuration the server refuses to guess at.
var ErrAmbiguousPromotionRule = errors.New("more than one promotion rule applies")

// ruleAppliesTo reports whether the rule actually covers the component: the
// component lives in the rule's from_repo and passes its path filter. This is
// the same test ListRulesForComponent offers rules by — but offering is not
// enforcing: Promote takes a caller-supplied (rule_id, component_id) pair, so
// without this check any caller with promotion permission could promote an
// arbitrary component with whichever rule has the laxest gates, bypassing a
// stricter rule's require_scan_pass/require_manual_approval on the pair that
// actually covers it (#255).
func (s *PromotionService) ruleAppliesTo(ctx context.Context, rule *domain.PromotionRule, comp *domain.Component) error {
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
	return s.noSiblingRuleApplies(ctx, rule, comp)
}

// noSiblingRuleApplies refuses a promotion that more than one rule covers.
//
// Confirming the named rule covers the component is not enough: two rules on
// the same repository pair can both cover it with different gates, and naming
// the laxer one then completes a promotion the stricter one's
// require_scan_pass/require_manual_approval would have blocked — no error, no
// warning, nothing recording that a gate was routed around (#366). Since the
// server cannot tell which rule the caller meant, the refusal is symmetric:
// naming either one is refused, including the stricter one, until an operator
// deletes or narrows a rule so exactly one applies.
func (s *PromotionService) noSiblingRuleApplies(ctx context.Context, rule *domain.PromotionRule, comp *domain.Component) error {
	siblings, err := s.promotionRepo.ListRulesByFromRepo(ctx, comp.Repository)
	if err != nil {
		return fmt.Errorf("cannot check whether other rules cover component %s: %w", comp.ID, err)
	}
	for i := range siblings {
		sib := siblings[i]
		if sib.ID == rule.ID || sib.ToRepo != rule.ToRepo {
			continue
		}
		matched, ferr := s.evalPathFilter(sib, comp)
		if ferr != nil {
			// A sibling whose filter cannot be evaluated may well cover this
			// component; letting the promotion through would be exactly the
			// bypass this check exists to prevent.
			return fmt.Errorf("%w: rule %q also promotes %q→%q and its path filter could not be evaluated: %w",
				ErrAmbiguousPromotionRule, sib.Name, sib.FromRepo, sib.ToRepo, ferr)
		}
		if matched {
			return fmt.Errorf("%w: rules %q and %q both cover component %s from %q to %q — "+
				"delete or narrow one of them, since naming either decides which gates apply",
				ErrAmbiguousPromotionRule, rule.Name, sib.Name, comp.ID, rule.FromRepo, rule.ToRepo)
		}
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
	// CreateRule's own emptiness check, which this sibling was missing. The
	// FromRepo == ToRepo test below happens to catch a rule with *both* sides
	// blank ("" == ""), but not one with a single side blank, so a PUT with
	// from_repo:"" persisted an orphaned rule that no promotion could ever
	// match.
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
	included := make(map[string]int, len(componentIDs))
	for _, compID := range componentIDs {
		comp, cerr := s.componentRepo.Get(ctx, compID)
		if cerr != nil || comp == nil {
			return nil, fmt.Errorf("component not found: %s", compID)
		}
		if aerr := s.ruleAppliesTo(ctx, rule, comp); aerr != nil {
			return nil, aerr
		}
		if serr := s.scanGate(ctx, rule, compID); serr != nil {
			return nil, serr
		}
		// An image missing a layer in the source is refused here, before a
		// request row exists, rather than failing later at copy time (#541).
		n, xerr := s.imageDependents(ctx, comp)
		if xerr != nil {
			return nil, xerr
		}
		included[compID] = n
		if perr := s.targetPolicyGate(ctx, rule, comp); perr != nil {
			return nil, perr
		}
	}

	var results []domain.PromotionRequest
	for _, compID := range componentIDs {
		req := &domain.PromotionRequest{
			RuleID:             ruleID,
			ComponentID:        compID,
			Status:             domain.PromotionPending,
			RequestedBy:        requestedByID,
			IncludedComponents: included[compID],
		}
		if err := s.promotionRepo.CreateRequest(ctx, req); err != nil {
			return nil, fmt.Errorf("create promotion request: %w", err)
		}

		if !rule.RequireManualApproval {
			// The same row lock Approve takes: the freshly created pending request
			// is already visible, and a concurrent manual approval racing this
			// auto-approve would otherwise copy twice.
			lockErr := s.promotionRepo.WithPendingRequestLock(ctx, req.ID,
				func(ctx context.Context, lockedReq *domain.PromotionRequest) repository.PromotionOutcome {
					now := time.Now()
					// Re-read the rule for every component: an administrator editing
					// it mid-batch (disabling a gate, narrowing the filter) must
					// govern the components still to be processed, exactly as it
					// would if each were approved individually.
					freshRule, rerr := s.promotionRepo.GetRule(ctx, ruleID)
					if rerr != nil || freshRule == nil {
						req.Status = domain.PromotionFailed
						req.Error = fmt.Sprintf("promotion rule not found: %s", ruleID)
						return repository.PromotionOutcome{Status: domain.PromotionFailed,
							CompletedAt: &now, Error: req.Error}
					}
					included, copyErr := s.executeCopy(ctx, lockedReq, freshRule)
					req.IncludedComponents = included
					if copyErr != nil {
						req.Status = domain.PromotionFailed
						req.Error = copyErr.Error()
						return repository.PromotionOutcome{Status: domain.PromotionFailed,
							CompletedAt: &now, Error: copyErr.Error()}
					}
					req.Status = domain.PromotionCompleted
					req.CompletedAt = &now
					return repository.PromotionOutcome{Status: domain.PromotionCompleted, CompletedAt: &now}
				})
			if lockErr != nil {
				// A concurrent reviewer settled this request first; report what the
				// row says now rather than inventing an outcome.
				if settled, gerr := s.promotionRepo.GetRequest(ctx, req.ID); gerr == nil && settled != nil {
					req = settled
				}
			}
		}

		results = append(results, *req)
	}
	return results, nil
}

// Approve approves a pending promotion request and copies the artifact. The
// pending check, the copy and the status write happen under the request row's
// lock: two concurrent approvals would otherwise both pass the check and both
// copy — double blob writes and a double-fired publish webhook.
func (s *PromotionService) Approve(ctx context.Context, requestID, reviewerID string) error {
	var copyErr error
	err := s.promotionRepo.WithPendingRequestLock(ctx, requestID,
		func(ctx context.Context, req *domain.PromotionRequest) repository.PromotionOutcome {
			now := time.Now()
			rule, rerr := s.promotionRepo.GetRule(ctx, req.RuleID)
			if rerr != nil || rule == nil {
				copyErr = fmt.Errorf("promotion rule not found: %s", req.RuleID)
				return repository.PromotionOutcome{Status: domain.PromotionFailed,
					ReviewedBy: &reviewerID, ReviewedAt: &now, CompletedAt: &now, Error: copyErr.Error()}
			}
			if _, cerr := s.executeCopy(ctx, req, rule); cerr != nil {
				copyErr = cerr
				return repository.PromotionOutcome{Status: domain.PromotionFailed,
					ReviewedBy: &reviewerID, ReviewedAt: &now, CompletedAt: &now, Error: cerr.Error()}
			}
			return repository.PromotionOutcome{Status: domain.PromotionCompleted,
				ReviewedBy: &reviewerID, ReviewedAt: &now, CompletedAt: &now}
		})
	if errors.Is(err, repository.ErrNotFound) {
		return fmt.Errorf("promotion request not found: %s", requestID)
	}
	if err != nil {
		return err
	}
	return copyErr
}

// Reject rejects a pending promotion request, under the same row lock Approve
// takes — a rejection racing an approval must lose to whichever committed
// first, not silently overwrite it.
func (s *PromotionService) Reject(ctx context.Context, requestID, reviewerID, reason string) error {
	err := s.promotionRepo.WithPendingRequestLock(ctx, requestID,
		func(_ context.Context, _ *domain.PromotionRequest) repository.PromotionOutcome {
			now := time.Now()
			return repository.PromotionOutcome{Status: domain.PromotionRejected,
				ReviewedBy: &reviewerID, ReviewedAt: &now, Error: reason}
		})
	if errors.Is(err, repository.ErrNotFound) {
		return fmt.Errorf("promotion request not found: %s", requestID)
	}
	return err
}

// executeCopy copies a component's blobs and metadata from from_repo to to_repo.
// For a Docker/OCI manifest it copies the whole image — digest alias, config
// and layer blobs, child manifests of an index — and returns how many such
// components came along beyond the one requested (see expandImage). The gates
// are the requested component's: the rule decides whether this image may be
// promoted, and the blobs are part of it, not separate artifacts a filter
// written for tags should be able to strip out of it.
//
// The copy is not transactional — the rows and blobs go one at a time — so a
// mid-copy failure compensates explicitly: every asset row and blob THIS call
// created fresh is removed, and a target component goes too once nothing
// references it. Only fresh creations roll back: both ComponentRepo.Create and
// AssetRepo.Create upsert on conflict, and blindly deleting "what I touched"
// after an upsert would destroy a legitimate pre-existing component from an
// earlier successful promotion of the same version. The one acknowledged
// residual: a pre-existing asset overwritten in place by this call's own upsert
// keeps the new content — point-in-time rollback of in-place updates is out of
// scope; what this prevents is the DB-visible half-populated component.
func (s *PromotionService) executeCopy(ctx context.Context, req *domain.PromotionRequest, rule *domain.PromotionRule) (included int, err error) {
	comp, err := s.componentRepo.Get(ctx, req.ComponentID)
	if err != nil || comp == nil {
		return 0, fmt.Errorf("source component not found: %s", req.ComponentID)
	}
	// Re-checked at copy time, not just when the request was filed: Approve
	// runs later, and the component may have moved, the rule changed, or a
	// scan found something while the request sat pending — the invariants
	// have to hold when the bytes actually move.
	if err := s.ruleAppliesTo(ctx, rule, comp); err != nil {
		return 0, err
	}
	if err := s.scanGate(ctx, rule, comp.ID); err != nil {
		return 0, err
	}
	toRepo, err := s.repoRepo.Get(ctx, rule.ToRepo)
	if err != nil || toRepo == nil {
		return 0, fmt.Errorf("target repository not found: %s", rule.ToRepo)
	}

	toStore, toBlobStoreID, err := s.resolveStore(ctx, toRepo.BlobStoreID)
	if err != nil {
		return 0, fmt.Errorf("target %s: %w", toRepo.Name, err)
	}

	assets, err := s.assetRepo.ListByComponentID(ctx, req.ComponentID)
	if err != nil {
		return 0, fmt.Errorf("list assets: %w", err)
	}

	// The whole image is resolved, and checked present in the source, before
	// the first byte moves: a missing layer fails the promotion with nothing
	// copied rather than leaving an unpullable half in the target.
	dependents, err := s.expandImage(ctx, comp, assets)
	if err != nil {
		return 0, err
	}
	units := append([]promotionUnit{{comp: comp, assets: assets}}, dependents...)

	// The target's write policy (#539) covers the whole expanded image and is
	// checked before the first byte moves, so a refusal copies nothing.
	guarded, err := s.checkTargetWritePolicy(ctx, toRepo, unitAssets(units))
	if err != nil {
		return 0, err
	}

	// Compensation state for the deferred rollback: only rows and blobs this
	// call created fresh (rows go before their bytes — a row whose blob is
	// already gone is not self-healing, an orphan blob is GC'd).
	var rb copyRollback
	defer func() {
		if err != nil {
			rb.run(ctx, s, toStore)
		}
	}()

	var mainComp *domain.Component
	for i, u := range units {
		pending := u.assets
		if u.contentAddressed {
			if pending, err = s.missingInTarget(ctx, toRepo.Name, u.assets); err != nil {
				return 0, err
			}
			if len(pending) == 0 {
				continue
			}
		}
		newComp := &domain.Component{
			RepositoryID: toRepo.ID,
			Repository:   toRepo.Name,
			Format:       string(toRepo.Format),
			Group:        u.comp.Group,
			Name:         u.comp.Name,
			Version:      u.comp.Version,
			Tags:         u.comp.Tags,
			Extra:        promotedExtra(u.comp.Extra),
		}
		if err = s.componentRepo.Create(ctx, newComp); err != nil {
			return 0, fmt.Errorf("upsert component in target: %w", err)
		}
		rb.compIDs = append(rb.compIDs, newComp.ID)
		if i == 0 {
			mainComp = newComp
		}
		for _, asset := range pending {
			g := guarded[asset.Path]
			if err = s.withTargetPathLock(ctx, g, base.BlobKey(toRepo.Name, asset.Path), func(ctx context.Context) error {
				if g {
					// A client push may have taken the path since the check above.
					if _, perr := base.CheckWritePolicy(ctx, s.assetRepo, toRepo, asset.Path); perr != nil {
						return fmt.Errorf("promote %s to %s: %w", asset.Path, toRepo.Name, perr)
					}
				}
				return s.copyAsset(ctx, asset, newComp, toRepo, toStore, toBlobStoreID, &rb)
			}); err != nil {
				return 0, err
			}
		}
	}

	if s.webhooks != nil {
		s.webhooks.Dispatch(domain.WebhookPayload{
			Event:      domain.EventArtifactPublished,
			Timestamp:  time.Now(),
			Repository: toRepo.Name,
			Component: map[string]any{
				"group":   mainComp.Group,
				"name":    mainComp.Name,
				"version": mainComp.Version,
				"format":  string(toRepo.Format),
			},
		})
	}
	return len(dependents), nil
}

// copyRollback records what one executeCopy created fresh in the target.
type copyRollback struct {
	assetIDs []string
	blobKeys []string
	compIDs  []string
}

// run removes the fresh rows, then their bytes, then every target component
// the copy touched that nothing references any more.
func (rb *copyRollback) run(ctx context.Context, s *PromotionService, toStore storage.BlobStore) {
	for _, id := range rb.assetIDs {
		_ = s.assetRepo.Delete(ctx, id)
	}
	for _, key := range rb.blobKeys {
		_ = toStore.Delete(ctx, key)
	}
	for _, id := range rb.compIDs {
		remaining, lerr := s.assetRepo.ListByComponentID(ctx, id)
		if lerr == nil && len(remaining) == 0 {
			_ = s.componentRepo.Delete(ctx, id)
		}
	}
}

// missingInTarget filters content-addressed assets down to those the target
// does not already hold. A path that names a digest and already carries the
// same SHA-256 there is the same bytes — from an earlier promotion of another
// tag sharing the layer, say — so it is skipped rather than rewritten.
func (s *PromotionService) missingInTarget(ctx context.Context, toRepo string, assets []domain.Asset) ([]domain.Asset, error) {
	var out []domain.Asset
	for _, a := range assets {
		existing, err := s.assetRepo.GetByPath(ctx, toRepo, a.Path)
		switch {
		case errors.Is(err, repository.ErrNotFound) || (err == nil && existing == nil):
			out = append(out, a)
		case err != nil:
			return nil, fmt.Errorf("check target asset %s: %w", a.Path, err)
		case a.SHA256 == "" || existing.SHA256 != a.SHA256:
			out = append(out, a)
		}
	}
	return out, nil
}

// copyAsset copies one source asset's bytes and row into newComp.
func (s *PromotionService) copyAsset(ctx context.Context, asset domain.Asset, newComp *domain.Component,
	toRepo *domain.Repository, toStore storage.BlobStore, toBlobStoreID string, rb *copyRollback) error {
	blobStoreID := asset.BlobStoreID
	fromStore, _, err := s.resolveStore(ctx, &blobStoreID)
	if err != nil {
		return fmt.Errorf("source asset %s: %w", asset.Path, err)
	}

	newBlobKey := base.BlobKey(toRepo.Name, asset.Path)

	// Fresh or pre-existing decides what the rollback may touch: a fresh
	// path's blob and row are this call's to delete; a pre-existing one was
	// only overwritten in place and must survive the compensation.
	_, preErr := s.assetRepo.GetByPath(ctx, toRepo.Name, asset.Path)
	fresh := errors.Is(preErr, repository.ErrNotFound)
	if preErr != nil && !fresh {
		return fmt.Errorf("check target asset %s: %w", asset.Path, preErr)
	}

	rc, size, err := fromStore.Get(ctx, asset.BlobKey)
	if err != nil {
		return fmt.Errorf("read blob %s: %w", asset.BlobKey, err)
	}
	if putErr := toStore.Put(ctx, newBlobKey, rc, size); putErr != nil {
		_ = rc.Close()
		return fmt.Errorf("write blob %s: %w", newBlobKey, putErr)
	}
	_ = rc.Close()
	if fresh {
		rb.blobKeys = append(rb.blobKeys, newBlobKey)
	}

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
	if fresh {
		rb.assetIDs = append(rb.assetIDs, newAsset.ID)
	}
	return nil
}

// unitAssets flattens every unit's assets: the full set a promotion writes.
func unitAssets(units []promotionUnit) []domain.Asset {
	var out []domain.Asset
	for _, u := range units {
		out = append(out, u.assets...)
	}
	return out
}

// checkTargetWritePolicy applies the target repository's write policy (#539)
// to every asset a promotion is about to copy, before the first byte moves, so
// a refused promotion leaves nothing half-copied: a read-only target refuses
// outright, a disable-redeploy one refuses when any non-exempt path is already
// taken there. Exempt paths (base.RedeployExempt) are digest-addressed OCI
// blobs and manifests, "latest" with allow_redeploy_latest, maven-metadata.xml
// and SNAPSHOTs — so an image whose layers the target already holds still
// promotes, and those identical units are then skipped by missingInTarget.
// The answer maps each path to whether its copy must hold the path lock
// (withTargetPathLock).
func (s *PromotionService) checkTargetWritePolicy(ctx context.Context, toRepo *domain.Repository, assets []domain.Asset) (map[string]bool, error) {
	guarded := make(map[string]bool, len(assets))
	for _, asset := range assets {
		g, err := base.CheckWritePolicy(ctx, s.assetRepo, toRepo, asset.Path)
		if err != nil {
			return nil, fmt.Errorf("promote %s to %s: %w", asset.Path, toRepo.Name, err)
		}
		guarded[asset.Path] = g
	}
	return guarded, nil
}

// targetPolicyGate is the Promote-time form of checkTargetWritePolicy: a
// promotion the target's write policy would refuse is refused before a
// request row is filed, the way #541 refuses an incomplete image. Approve
// checks again at copy time, since the target can change in between.
func (s *PromotionService) targetPolicyGate(ctx context.Context, rule *domain.PromotionRule, comp *domain.Component) error {
	toRepo, _ := s.repoRepo.Get(ctx, rule.ToRepo)
	if toRepo == nil {
		// Not this gate's call: a missing target fails at copy time, as it
		// did before the write policy existed.
		return nil
	}
	if domain.RepoWritePolicy(toRepo) == domain.WritePolicyAllow {
		return nil
	}
	assets, err := s.assetRepo.ListByComponentID(ctx, comp.ID)
	if err != nil {
		return fmt.Errorf("list assets: %w", err)
	}
	dependents, err := s.expandImage(ctx, comp, assets)
	if err != nil {
		return err
	}
	units := append([]promotionUnit{{comp: comp, assets: assets}}, dependents...)
	_, err = s.checkTargetWritePolicy(ctx, toRepo, unitAssets(units))
	return err
}

// withTargetPathLock runs one asset's copy under the target path's blob-key
// lock when the write policy guards that path — the same lock a client push
// of the path takes in base.StoreArtifact, so a promotion and a push cannot
// both claim a fresh path.
func (s *PromotionService) withTargetPathLock(ctx context.Context, guarded bool, blobKey string, copyOne func(context.Context) error) error {
	if !guarded {
		return copyOne(ctx)
	}
	return s.assetRepo.WithBlobKeyLock(ctx, blobKey, copyOne)
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

// resolveStore returns the physical BlobStore for a given blobStoreID
// pointer, and the id to record on the copied assets.
//
// Both branches resolve the physical store from the DB row, the way every
// other data path does: in a stock local deployment the seeded default row's
// store lives under its own subdirectory, not at the process store's root, so
// answering the implicit-default case with the injected default instance
// would write the copied blobs where no download will ever look for them.
// The id is never empty — assets.blob_store_id is a NOT NULL foreign key,
// and the old empty-string answer made every promotion touching a
// default-store repository fail with a raw constraint error (#256).
func (s *PromotionService) resolveStore(ctx context.Context, blobStoreID *string) (storage.BlobStore, string, error) {
	implicit := blobStoreID == nil || *blobStoreID == ""
	var bsMeta *domain.BlobStore
	var err error
	if implicit {
		bsMeta, err = repository.DefaultBlobStore(ctx, s.blobRepo)
		if err != nil {
			return nil, "", err
		}
	} else {
		bsMeta, err = s.blobRepo.GetByID(ctx, *blobStoreID)
		if errors.Is(err, repository.ErrNotFound) || (err == nil && bsMeta == nil) {
			return nil, "", fmt.Errorf("blob store id %q not found", *blobStoreID)
		}
		if err != nil {
			return nil, "", fmt.Errorf("blob store: %w", err)
		}
	}
	bs, err := s.blobResolver.Get(ctx, storage.BlobStoreDescriptor{
		ID:     bsMeta.ID,
		Type:   bsMeta.Type,
		Config: bsMeta.Config,
	})
	if err != nil {
		return nil, "", fmt.Errorf("blob store %q: %w", bsMeta.Name, err)
	}
	return bs, bsMeta.ID, nil
}
