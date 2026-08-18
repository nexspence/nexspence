package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// Promote takes a caller-supplied (rule_id, component_id) pair, so offering
// only matching rules in ListRulesForComponent is not enforcement: without a
// re-check, any caller with promotion permission could promote an arbitrary
// component with whichever rule has the laxest gates (#255).
func TestPromotionService_Promote_RefusesRuleFromAnotherRepo(t *testing.T) {
	svc, promoRepo, compRepo, _, _, repoRepo, _, _ := newTestPromotionSvc(t)
	ctx := context.Background()

	repoRepo.Create(ctx, testutil.SimpleRepo("staging", "raw"))
	repoRepo.Create(ctx, testutil.SimpleRepo("production", "raw"))
	repoRepo.Create(ctx, testutil.SimpleRepo("sandbox", "raw"))

	// The component lives in sandbox; the (lax, no-gates) rule promotes from
	// staging. A stricter staging←sandbox rule with gates is beside the point —
	// the attack is exactly that the caller picked a rule that skips them.
	comp := &domain.Component{
		ID: "comp-elsewhere", Repository: "sandbox", Format: "raw",
		Group: "com/example", Name: "mylib", Version: "1.0.0",
	}
	compRepo.AddComponent(comp)

	rule := &domain.PromotionRule{Name: "lax", FromRepo: "staging", ToRepo: "production"}
	if err := promoRepo.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	_, err := svc.Promote(ctx, rule.ID, []string{comp.ID}, "user-1")
	if err == nil || !strings.Contains(err.Error(), `does not promote from`) {
		t.Fatalf("expected from_repo mismatch refusal, got: %v", err)
	}

	// No request row may exist for the refused promotion — a pending request
	// nobody could legitimately approve is just a trap for a reviewer.
	reqs, lerr := promoRepo.ListRequests(ctx, "")
	if lerr != nil {
		t.Fatalf("ListRequests: %v", lerr)
	}
	if len(reqs) != 0 {
		t.Fatalf("expected no promotion requests after refusal, got %d", len(reqs))
	}
}

func TestPromotionService_Promote_RefusesComponentOutsidePathFilter(t *testing.T) {
	svc, promoRepo, compRepo, _, _, repoRepo, _, _ := newTestPromotionSvc(t)
	ctx := context.Background()

	repoRepo.Create(ctx, testutil.SimpleRepo("staging", "raw"))
	repoRepo.Create(ctx, testutil.SimpleRepo("production", "raw"))

	comp := &domain.Component{
		ID: "comp-outside", Repository: "staging", Format: "raw",
		Group: "com/other", Name: "mylib", Version: "1.0.0",
	}
	compRepo.AddComponent(comp)

	rule := &domain.PromotionRule{
		Name: "scoped", FromRepo: "staging", ToRepo: "production",
		PathFilter: `path.startsWith("/com/example/")`,
	}
	if err := promoRepo.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	_, err := svc.Promote(ctx, rule.ID, []string{comp.ID}, "user-1")
	if err == nil || !strings.Contains(err.Error(), "path filter") {
		t.Fatalf("expected path-filter refusal, got: %v", err)
	}
}

// Approve runs later than the request was filed, and the component may have
// been moved (or re-created elsewhere) in between: the invariant has to hold
// when the bytes actually move, so executeCopy re-checks it.
func TestPromotionService_Approve_RefusesWhenComponentNoLongerApplies(t *testing.T) {
	svc, promoRepo, compRepo, _, _, repoRepo, _, _ := newTestPromotionSvc(t)
	ctx := context.Background()

	repoRepo.Create(ctx, testutil.SimpleRepo("staging", "raw"))
	repoRepo.Create(ctx, testutil.SimpleRepo("production", "raw"))

	comp := &domain.Component{
		ID: "comp-moving", Repository: "staging", Format: "raw",
		Group: "com/example", Name: "mylib", Version: "1.0.0",
	}
	compRepo.AddComponent(comp)

	rule := &domain.PromotionRule{
		Name: "gated", FromRepo: "staging", ToRepo: "production",
		RequireManualApproval: true,
	}
	if err := promoRepo.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	reqs, err := svc.Promote(ctx, rule.ID, []string{comp.ID}, "user-1")
	if err != nil || len(reqs) != 1 {
		t.Fatalf("Promote: %v (%d requests)", err, len(reqs))
	}

	// The component moves out of staging while the request sits pending.
	comp.Repository = "production"

	err = svc.Approve(ctx, reqs[0].ID, "admin-1")
	if err == nil || !strings.Contains(err.Error(), "does not promote from") {
		t.Fatalf("expected Approve to surface the from_repo mismatch, got: %v", err)
	}
	got, err := promoRepo.GetRequest(ctx, reqs[0].ID)
	if err != nil || got == nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if got.Status != domain.PromotionFailed {
		t.Fatalf("status: got %s, want failed", got.Status)
	}
	if !strings.Contains(got.Error, "does not promote from") {
		t.Fatalf("failure reason %q does not name the from_repo mismatch", got.Error)
	}
}
