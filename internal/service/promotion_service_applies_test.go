package service_test

import (
	"context"
	"strings"
	"testing"
	"time"

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

// With an auto-approve rule the execution loop copies as it goes, so batch
// validation must run up front: a mid-batch refusal after copies had executed
// would tell the caller "failed" while artifacts were already promoted.
func TestPromotionService_Promote_BatchRefusedBeforeAnyCopy(t *testing.T) {
	svc, promoRepo, compRepo, _, blobStore, repoRepo, _, _ := newTestPromotionSvc(t)
	ctx := context.Background()

	repoRepo.Create(ctx, testutil.SimpleRepo("staging", "raw"))
	repoRepo.Create(ctx, testutil.SimpleRepo("production", "raw"))
	repoRepo.Create(ctx, testutil.SimpleRepo("sandbox", "raw"))

	okComp := &domain.Component{
		ID: "comp-ok", Repository: "staging", Format: "raw",
		Group: "com/example", Name: "mylib", Version: "1.0.0",
	}
	badComp := &domain.Component{
		ID: "comp-bad", Repository: "sandbox", Format: "raw",
		Group: "com/example", Name: "otherlib", Version: "1.0.0",
	}
	compRepo.AddComponent(okComp)
	compRepo.AddComponent(badComp)

	rule := &domain.PromotionRule{Name: "auto", FromRepo: "staging", ToRepo: "production"}
	if err := promoRepo.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	_, err := svc.Promote(ctx, rule.ID, []string{okComp.ID, badComp.ID}, "user-1")
	if err == nil {
		t.Fatal("expected the batch to be refused")
	}
	// Nothing may have been created or copied for the valid prefix either.
	reqs, _ := promoRepo.ListRequests(ctx, "")
	if len(reqs) != 0 {
		t.Fatalf("expected no requests after batch refusal, got %d", len(reqs))
	}
	if keys, kerr := blobStore.ListKeys(ctx); kerr != nil || len(keys) != 0 {
		t.Fatalf("expected no blobs copied after batch refusal, got %v (%v)", keys, kerr)
	}
}

// A rule whose filter is broken (evaluates to a non-bool, or errors) must
// fail closed with an error pointing at the FILTER, not at the component.
func TestPromotionService_Promote_BrokenFilterNamesTheFilter(t *testing.T) {
	svc, promoRepo, compRepo, _, _, repoRepo, _, _ := newTestPromotionSvc(t)
	ctx := context.Background()

	repoRepo.Create(ctx, testutil.SimpleRepo("staging", "raw"))
	repoRepo.Create(ctx, testutil.SimpleRepo("production", "raw"))
	comp := &domain.Component{
		ID: "comp-1", Repository: "staging", Format: "raw",
		Group: "com/example", Name: "mylib", Version: "1.0.0",
	}
	compRepo.AddComponent(comp)

	// path.matches("(") compiles (the regexp is a runtime value) but errors at
	// eval — the class of broken filter CreateRule cannot catch.
	rule := &domain.PromotionRule{
		Name: "broken", FromRepo: "staging", ToRepo: "production",
		PathFilter: `path.matches("(")`,
	}
	if err := promoRepo.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	_, err := svc.Promote(ctx, rule.ID, []string{comp.ID}, "user-1")
	if err == nil || !strings.Contains(err.Error(), "path filter failed to evaluate") {
		t.Fatalf("expected a filter-evaluation error naming the filter, got: %v", err)
	}
}

// CreateRule/UpdateRule refuse a filter that compiles to a non-bool: it would
// fail every applicability check downstream while looking valid.
func TestPromotionService_CreateRule_NonBoolFilterRejected(t *testing.T) {
	svc, _, _, _, _, _, _, _ := newTestPromotionSvc(t)
	ctx := context.Background()

	err := svc.CreateRule(ctx, &domain.PromotionRule{
		Name: "non-bool", FromRepo: "staging", ToRepo: "production",
		PathFilter: `path`,
	})
	if err == nil || !strings.Contains(err.Error(), "must evaluate to a boolean") {
		t.Fatalf("expected non-bool filter rejection, got: %v", err)
	}
}

// The scan gate is re-evaluated when the bytes actually move: a scan that
// found something while the request sat pending must stop the approval.
func TestPromotionService_Approve_ReRunsScanGate(t *testing.T) {
	svc, promoRepo, compRepo, _, _, repoRepo, _, scanRepo := newTestPromotionSvc(t)
	ctx := context.Background()

	repoRepo.Create(ctx, testutil.SimpleRepo("staging", "raw"))
	repoRepo.Create(ctx, testutil.SimpleRepo("production", "raw"))
	comp := &domain.Component{
		ID: "comp-scan", Repository: "staging", Format: "raw",
		Group: "com/example", Name: "mylib", Version: "1.0.0",
	}
	compRepo.AddComponent(comp)

	rule := &domain.PromotionRule{
		Name: "gated", FromRepo: "staging", ToRepo: "production",
		RequireScanPass: true, RequireManualApproval: true,
	}
	if err := promoRepo.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	// Clean at request time…
	if err := scanRepo.Insert(ctx, &domain.ScanResultRow{ComponentID: comp.ID, Scanner: "osv", Status: "completed", ScannedAt: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatalf("Insert scan: %v", err)
	}
	reqs, err := svc.Promote(ctx, rule.ID, []string{comp.ID}, "user-1")
	if err != nil || len(reqs) != 1 {
		t.Fatalf("Promote: %v (%d requests)", err, len(reqs))
	}

	// …dirty by approval time.
	if err := scanRepo.Insert(ctx, &domain.ScanResultRow{ComponentID: comp.ID, Scanner: "osv", Status: "completed", Critical: 1, ScannedAt: time.Now()}); err != nil {
		t.Fatalf("Insert dirty scan: %v", err)
	}

	err = svc.Approve(ctx, reqs[0].ID, "admin-1")
	if err == nil || !strings.Contains(err.Error(), "critical") {
		t.Fatalf("expected the approval to hit the scan gate, got: %v", err)
	}
	got, _ := promoRepo.GetRequest(ctx, reqs[0].ID)
	if got == nil || got.Status != domain.PromotionFailed {
		t.Fatalf("request status: got %v, want failed", got)
	}
}
