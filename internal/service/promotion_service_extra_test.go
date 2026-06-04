package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// newPromotionSvcExtra reuses newTestPromotionSvc (defined in promotion_service_test.go).
// It is a thin wrapper so test bodies read clearly.

// ── ListRules ────────────────────────────────────────────────────────────────

func TestPromotionService_ListRules_Empty(t *testing.T) {
	svc, _, _, _, _, _, _, _ := newTestPromotionSvc(t)
	rules, err := svc.ListRules(context.Background())
	if err != nil {
		t.Fatalf("ListRules: %v", err)
	}
	if len(rules) != 0 {
		t.Errorf("expected 0 rules, got %d", len(rules))
	}
}

func TestPromotionService_ListRules_Populated(t *testing.T) {
	svc, promoRepo, _, _, _, _, _, _ := newTestPromotionSvc(t)
	ctx := context.Background()

	if err := promoRepo.CreateRule(ctx, &domain.PromotionRule{
		Name:     "rule-one",
		FromRepo: "dev",
		ToRepo:   "staging",
	}); err != nil {
		t.Fatalf("CreateRule seed: %v", err)
	}
	if err := promoRepo.CreateRule(ctx, &domain.PromotionRule{
		Name:     "rule-two",
		FromRepo: "staging",
		ToRepo:   "prod",
	}); err != nil {
		t.Fatalf("CreateRule seed: %v", err)
	}

	rules, err := svc.ListRules(ctx)
	if err != nil {
		t.Fatalf("ListRules: %v", err)
	}
	if len(rules) != 2 {
		t.Errorf("expected 2 rules, got %d", len(rules))
	}
}

// ── GetRule ───────────────────────────────────────────────────────────────────

func TestPromotionService_GetRule_Found(t *testing.T) {
	svc, promoRepo, _, _, _, _, _, _ := newTestPromotionSvc(t)
	ctx := context.Background()

	rule := &domain.PromotionRule{
		Name:     "get-test-rule",
		FromRepo: "a",
		ToRepo:   "b",
	}
	if err := promoRepo.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	got, err := svc.GetRule(ctx, rule.ID)
	if err != nil {
		t.Fatalf("GetRule: %v", err)
	}
	if got == nil {
		t.Fatal("expected rule, got nil")
	}
	if got.Name != "get-test-rule" {
		t.Errorf("expected name %q, got %q", "get-test-rule", got.Name)
	}
}

func TestPromotionService_GetRule_NotFound_ReturnsNil(t *testing.T) {
	svc, _, _, _, _, _, _, _ := newTestPromotionSvc(t)
	got, err := svc.GetRule(context.Background(), "nonexistent-id")
	if err != nil {
		t.Fatalf("GetRule: unexpected error %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for unknown rule, got %+v", got)
	}
}

// ── ListRequests ─────────────────────────────────────────────────────────────

func TestPromotionService_ListRequests_Empty(t *testing.T) {
	svc, _, _, _, _, _, _, _ := newTestPromotionSvc(t)
	reqs, err := svc.ListRequests(context.Background(), "")
	if err != nil {
		t.Fatalf("ListRequests: %v", err)
	}
	if len(reqs) != 0 {
		t.Errorf("expected 0 requests, got %d", len(reqs))
	}
}

func TestPromotionService_ListRequests_FilterByStatus(t *testing.T) {
	svc, promoRepo, compRepo, _, _, repoRepo, _, _ := newTestPromotionSvc(t)
	ctx := context.Background()

	fromRepo := testutil.SimpleRepo("src", "raw")
	toRepo := testutil.SimpleRepo("dst", "raw")
	repoRepo.Create(ctx, fromRepo)
	repoRepo.Create(ctx, toRepo)

	comp := &domain.Component{
		ID: "comp-list-req", Repository: fromRepo.Name, Format: "raw",
		Group: "g", Name: "n", Version: "1.0",
	}
	compRepo.AddComponent(comp)

	// Create a manual-approval rule so request stays pending.
	rule := &domain.PromotionRule{
		Name:                  "list-req-rule",
		FromRepo:              fromRepo.Name,
		ToRepo:                toRepo.Name,
		RequireManualApproval: true,
	}
	if err := promoRepo.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	if _, err := svc.Promote(ctx, rule.ID, []string{comp.ID}, "user-x"); err != nil {
		t.Fatalf("Promote: %v", err)
	}

	// Filter pending — should get 1.
	pending, err := svc.ListRequests(ctx, string(domain.PromotionPending))
	if err != nil {
		t.Fatalf("ListRequests pending: %v", err)
	}
	if len(pending) != 1 {
		t.Errorf("expected 1 pending, got %d", len(pending))
	}

	// Filter completed — should get 0.
	completed, err := svc.ListRequests(ctx, string(domain.PromotionCompleted))
	if err != nil {
		t.Fatalf("ListRequests completed: %v", err)
	}
	if len(completed) != 0 {
		t.Errorf("expected 0 completed, got %d", len(completed))
	}
}

// ── Approve error branches ────────────────────────────────────────────────────

func TestPromotionService_Approve_RequestNotFound(t *testing.T) {
	svc, _, _, _, _, _, _, _ := newTestPromotionSvc(t)
	err := svc.Approve(context.Background(), "no-such-id", "reviewer")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error, got %v", err)
	}
}

func TestPromotionService_Approve_NotPendingStatus(t *testing.T) {
	svc, promoRepo, compRepo, _, _, repoRepo, _, _ := newTestPromotionSvc(t)
	ctx := context.Background()

	fromRepo := testutil.SimpleRepo("from-approve", "raw")
	toRepo := testutil.SimpleRepo("to-approve", "raw")
	repoRepo.Create(ctx, fromRepo)
	repoRepo.Create(ctx, toRepo)

	comp := &domain.Component{
		ID: "comp-approve-np", Repository: fromRepo.Name, Format: "raw",
		Group: "g", Name: "n", Version: "1",
	}
	compRepo.AddComponent(comp)

	rule := &domain.PromotionRule{
		Name: "approve-np-rule", FromRepo: fromRepo.Name, ToRepo: toRepo.Name,
		RequireManualApproval: true,
	}
	if err := promoRepo.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	results, err := svc.Promote(ctx, rule.ID, []string{comp.ID}, "user-y")
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	reqID := results[0].ID

	// Reject first to move it out of pending.
	if err := svc.Reject(ctx, reqID, "rev", "reason"); err != nil {
		t.Fatalf("Reject: %v", err)
	}

	// Now approve should fail with "not pending".
	err = svc.Approve(ctx, reqID, "rev2")
	if err == nil || !strings.Contains(err.Error(), "not pending") {
		t.Fatalf("expected not-pending error, got %v", err)
	}
}

// ── Reject error branches ─────────────────────────────────────────────────────

func TestPromotionService_Reject_RequestNotFound(t *testing.T) {
	svc, _, _, _, _, _, _, _ := newTestPromotionSvc(t)
	err := svc.Reject(context.Background(), "no-such-id", "reviewer", "reason")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error, got %v", err)
	}
}

func TestPromotionService_Reject_NotPendingStatus(t *testing.T) {
	svc, promoRepo, compRepo, assetRepo, blobStore, repoRepo, _, _ := newTestPromotionSvc(t)
	ctx := context.Background()

	fromRepo := testutil.SimpleRepo("from-reject", "raw")
	toRepo := testutil.SimpleRepo("to-reject", "raw")
	repoRepo.Create(ctx, fromRepo)
	repoRepo.Create(ctx, toRepo)

	comp := &domain.Component{
		ID: "comp-reject-np", Repository: fromRepo.Name, Format: "raw",
		Group: "g", Name: "art", Version: "1.0",
	}
	compRepo.AddComponent(comp)

	blobKey := fromRepo.Name + ":art-1.0.jar"
	if err := blobStore.PutBytes(ctx, blobKey, []byte("data")); err != nil {
		t.Fatalf("PutBytes: %v", err)
	}
	assetRepo.Create(ctx, &domain.Asset{
		ComponentID: comp.ID, RepositoryID: fromRepo.ID, Repository: fromRepo.Name,
		Path: "art-1.0.jar", BlobKey: blobKey, SizeBytes: 4,
	})

	// Auto-approve rule so request completes immediately.
	rule := &domain.PromotionRule{
		Name:                  "auto-reject-test",
		FromRepo:              fromRepo.Name,
		ToRepo:                toRepo.Name,
		RequireManualApproval: false,
	}
	if err := promoRepo.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	results, err := svc.Promote(ctx, rule.ID, []string{comp.ID}, "user-z")
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	reqID := results[0].ID

	// Request is completed — Reject must fail.
	err = svc.Reject(ctx, reqID, "rev", "nope")
	if err == nil || !strings.Contains(err.Error(), "not pending") {
		t.Fatalf("expected not-pending error, got %v", err)
	}
}

// ── ListRulesForComponent (GetComponentPromotionRules) ────────────────────────

func TestPromotionService_ListRulesForComponent_ComponentNotFound(t *testing.T) {
	svc, _, _, _, _, _, _, _ := newTestPromotionSvc(t)
	_, err := svc.ListRulesForComponent(context.Background(), "no-comp")
	if err == nil || !strings.Contains(err.Error(), "component not found") {
		t.Fatalf("expected component not found error, got %v", err)
	}
}

func TestPromotionService_ListRulesForComponent_NoMatchingRules(t *testing.T) {
	svc, _, compRepo, _, _, _, _, _ := newTestPromotionSvc(t)
	ctx := context.Background()

	comp := &domain.Component{
		ID: "comp-no-rules", Repository: "staging",
		Format: "raw", Group: "g", Name: "x", Version: "1",
	}
	compRepo.AddComponent(comp)

	rules, err := svc.ListRulesForComponent(ctx, comp.ID)
	if err != nil {
		t.Fatalf("ListRulesForComponent: %v", err)
	}
	if len(rules) != 0 {
		t.Errorf("expected 0 rules, got %d", len(rules))
	}
}

func TestPromotionService_ListRulesForComponent_ReturnsMatchingRule(t *testing.T) {
	svc, promoRepo, compRepo, _, _, _, _, _ := newTestPromotionSvc(t)
	ctx := context.Background()

	comp := &domain.Component{
		ID: "comp-with-rules", Repository: "staging",
		Format: "raw", Group: "g", Name: "y", Version: "2",
	}
	compRepo.AddComponent(comp)

	rule := &domain.PromotionRule{
		Name: "match-rule", FromRepo: "staging", ToRepo: "prod",
	}
	if err := promoRepo.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	rules, err := svc.ListRulesForComponent(ctx, comp.ID)
	if err != nil {
		t.Fatalf("ListRulesForComponent: %v", err)
	}
	if len(rules) != 1 {
		t.Errorf("expected 1 rule, got %d", len(rules))
	}
	if rules[0].ID != rule.ID {
		t.Errorf("expected rule ID %q, got %q", rule.ID, rules[0].ID)
	}
}

// ── UpdateRule ────────────────────────────────────────────────────────────────

func TestPromotionService_UpdateRule_Validation(t *testing.T) {
	svc, _, _, _, _, _, _, _ := newTestPromotionSvc(t)
	ctx := context.Background()

	t.Run("empty name", func(t *testing.T) {
		err := svc.UpdateRule(ctx, &domain.PromotionRule{
			ID:       "any",
			FromRepo: "a",
			ToRepo:   "b",
		})
		if err == nil || !strings.Contains(err.Error(), "name is required") {
			t.Fatalf("expected name required, got %v", err)
		}
	})

	t.Run("same repos", func(t *testing.T) {
		err := svc.UpdateRule(ctx, &domain.PromotionRule{
			ID:       "any",
			Name:     "r",
			FromRepo: "x",
			ToRepo:   "x",
		})
		if err == nil || !strings.Contains(err.Error(), "must be different") {
			t.Fatalf("expected same-repo error, got %v", err)
		}
	})

	t.Run("invalid CEL", func(t *testing.T) {
		err := svc.UpdateRule(ctx, &domain.PromotionRule{
			ID:         "any",
			Name:       "r",
			FromRepo:   "a",
			ToRepo:     "b",
			PathFilter: "!!!bad CEL!!!",
		})
		if err == nil || !strings.Contains(err.Error(), "path_filter") {
			t.Fatalf("expected CEL error, got %v", err)
		}
	})
}

// ── DeleteRule ─────────────────────────────────────────────────────────────────

func TestPromotionService_DeleteRule(t *testing.T) {
	svc, promoRepo, _, _, _, _, _, _ := newTestPromotionSvc(t)
	ctx := context.Background()

	rule := &domain.PromotionRule{
		Name: "delete-me", FromRepo: "a", ToRepo: "b",
	}
	if err := promoRepo.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	if err := svc.DeleteRule(ctx, rule.ID); err != nil {
		t.Fatalf("DeleteRule: %v", err)
	}

	got, err := svc.GetRule(ctx, rule.ID)
	if err != nil {
		t.Fatalf("GetRule after delete: %v", err)
	}
	if got != nil {
		t.Error("expected nil after delete, got a rule")
	}
}

// ── Promote scan-pass gate ─────────────────────────────────────────────────────

func TestPromotionService_Promote_ScanRequired_NoScan(t *testing.T) {
	svc, promoRepo, compRepo, _, _, repoRepo, _, _ := newTestPromotionSvc(t)
	ctx := context.Background()

	fromRepo := testutil.SimpleRepo("scan-src", "raw")
	toRepo := testutil.SimpleRepo("scan-dst", "raw")
	repoRepo.Create(ctx, fromRepo)
	repoRepo.Create(ctx, toRepo)

	comp := &domain.Component{
		ID: "comp-scan", Repository: fromRepo.Name, Format: "raw",
		Group: "g", Name: "s", Version: "1",
	}
	compRepo.AddComponent(comp)

	rule := &domain.PromotionRule{
		Name:            "scan-gate",
		FromRepo:        fromRepo.Name,
		ToRepo:          toRepo.Name,
		RequireScanPass: true,
	}
	if err := promoRepo.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	_, err := svc.Promote(ctx, rule.ID, []string{comp.ID}, "user")
	if err == nil || !strings.Contains(err.Error(), "scan required") {
		t.Fatalf("expected scan-required error, got %v", err)
	}
}
