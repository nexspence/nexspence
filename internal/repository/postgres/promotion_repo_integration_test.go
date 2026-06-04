//go:build integration

package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/testutil/pgtest"
)

// ── helpers ──────────────────────────────────────────────────────────────────

// promotionParents holds the FK-parent identifiers a promotion rule/request needs.
type promotionParents struct {
	FromRepo   string // repositories.name
	FromRepoID string // repositories.id (owning repo for created components)
	ToRepo     string // repositories.name
	UserID     string // users.id
}

// makePromotionParents creates the blob_store → two repositories → user chain
// required to satisfy promotion_rules / promotion_requests foreign keys.
// suffix must be unique within the test run.
func makePromotionParents(t *testing.T, ctx context.Context, suffix string) promotionParents {
	t.Helper()
	pool := pgtest.Pool(t)

	bsID := newRepoBlobStore(t, ctx, "promo_bs_"+suffix)

	rRepo := NewRepositoryRepo(pool)
	fromName := "promo_from_" + suffix
	toName := "promo_to_" + suffix
	from := makeRepo(fromName, domain.FormatRaw, domain.TypeHosted, strPtr(bsID))
	if err := rRepo.Create(ctx, from); err != nil {
		t.Fatalf("makePromotionParents: from repo: %v", err)
	}
	to := makeRepo(toName, domain.FormatRaw, domain.TypeHosted, strPtr(bsID))
	if err := rRepo.Create(ctx, to); err != nil {
		t.Fatalf("makePromotionParents: to repo: %v", err)
	}

	uRepo := NewUserRepo(pool)
	u := makeUser("promo_user_"+suffix, "promo_"+suffix+"@test.com")
	if err := uRepo.Create(ctx, u); err != nil {
		t.Fatalf("makePromotionParents: user: %v", err)
	}

	return promotionParents{
		FromRepo:   fromName,
		FromRepoID: from.ID,
		ToRepo:     toName,
		UserID:     u.ID,
	}
}

// makePromotionRule builds a PromotionRule referencing the given parent repos.
func makePromotionRule(name string, p promotionParents) *domain.PromotionRule {
	return &domain.PromotionRule{
		Name:                  name,
		FromRepo:              p.FromRepo,
		ToRepo:                p.ToRepo,
		PathFilter:            `path.startsWith("releases/")`,
		RequireScanPass:       true,
		RequireManualApproval: true,
	}
}

// makePromotionComponent inserts a component row (FK target for requests) into
// the given repository and returns its ID.
func makePromotionComponent(t *testing.T, ctx context.Context, repoID, version string) string {
	t.Helper()
	pool := pgtest.Pool(t)

	cRepo := NewComponentRepo(pool)
	c := &domain.Component{
		RepositoryID: repoID,
		Format:       "raw",
		Group:        "com.example",
		Name:         "promo-artifact",
		Version:      version,
	}
	if err := cRepo.Create(ctx, c); err != nil {
		t.Fatalf("makePromotionComponent: create component: %v", err)
	}
	return c.ID
}

const promoZeroUUID = "00000000-0000-0000-0000-000000000000"

// promoTables lists every table touched (incl. FK parents) for full isolation.
// Order does not matter — TRUNCATE ... CASCADE handles dependents.
var promoTables = []string{
	"promotion_requests", "promotion_rules",
	"components", "repositories", "blob_stores", "users",
}

// ── Rules: Create ─────────────────────────────────────────────────────────────

func TestPromotionRepo_CreateRule_PopulatesIDAndTimestamps(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, promoTables...)
	ctx := context.Background()
	repo := NewPromotionRepo(pool)

	p := makePromotionParents(t, ctx, "cr_ts")
	rule := makePromotionRule("rule_cr_ts", p)
	if err := repo.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	if rule.ID == "" {
		t.Fatal("CreateRule did not populate ID")
	}
	if rule.CreatedAt.IsZero() {
		t.Error("CreateRule did not populate CreatedAt")
	}
	if rule.UpdatedAt.IsZero() {
		t.Error("CreateRule did not populate UpdatedAt")
	}
}

func TestPromotionRepo_CreateRule_FieldsRoundTrip(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, promoTables...)
	ctx := context.Background()
	repo := NewPromotionRepo(pool)

	p := makePromotionParents(t, ctx, "cr_rt")
	rule := makePromotionRule("rule_cr_rt", p)
	if err := repo.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	got, err := repo.GetRule(ctx, rule.ID)
	if err != nil {
		t.Fatalf("GetRule: %v", err)
	}
	if got == nil {
		t.Fatal("GetRule returned nil for existing rule")
	}
	if got.Name != "rule_cr_rt" {
		t.Errorf("Name: got %q, want rule_cr_rt", got.Name)
	}
	if got.FromRepo != p.FromRepo {
		t.Errorf("FromRepo: got %q, want %q", got.FromRepo, p.FromRepo)
	}
	if got.ToRepo != p.ToRepo {
		t.Errorf("ToRepo: got %q, want %q", got.ToRepo, p.ToRepo)
	}
	if got.PathFilter != `path.startsWith("releases/")` {
		t.Errorf("PathFilter: got %q, want path.startsWith(...)", got.PathFilter)
	}
	if !got.RequireScanPass {
		t.Error("RequireScanPass: got false, want true")
	}
	if !got.RequireManualApproval {
		t.Error("RequireManualApproval: got false, want true")
	}
}

func TestPromotionRepo_CreateRule_EmptyPathFilterStoresNull(t *testing.T) {
	// PathFilter "" must round-trip as "" (stored as SQL NULL).
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, promoTables...)
	ctx := context.Background()
	repo := NewPromotionRepo(pool)

	p := makePromotionParents(t, ctx, "cr_nullpf")
	rule := makePromotionRule("rule_cr_nullpf", p)
	rule.PathFilter = ""
	rule.RequireScanPass = false
	rule.RequireManualApproval = false
	if err := repo.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	got, err := repo.GetRule(ctx, rule.ID)
	if err != nil {
		t.Fatalf("GetRule: %v", err)
	}
	if got == nil {
		t.Fatal("GetRule returned nil")
	}
	if got.PathFilter != "" {
		t.Errorf("PathFilter: got %q, want empty", got.PathFilter)
	}
	if got.RequireScanPass {
		t.Error("RequireScanPass: got true, want false")
	}
	if got.RequireManualApproval {
		t.Error("RequireManualApproval: got true, want false")
	}
}

// ── Rules: Get ────────────────────────────────────────────────────────────────

func TestPromotionRepo_GetRule_NotFound_ReturnsNilNil(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, promoTables...)
	ctx := context.Background()
	repo := NewPromotionRepo(pool)

	got, err := repo.GetRule(ctx, promoZeroUUID)
	if err != nil {
		t.Fatalf("GetRule(missing): got err %v, want nil", err)
	}
	if got != nil {
		t.Errorf("GetRule(missing): got %+v, want nil", got)
	}
}

// ── Rules: List ───────────────────────────────────────────────────────────────

func TestPromotionRepo_ListRules_OrderedByName(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, promoTables...)
	ctx := context.Background()
	repo := NewPromotionRepo(pool)

	p := makePromotionParents(t, ctx, "lr_order")
	// Insert out of alphabetical order; expect name-ASC output.
	for _, name := range []string{"rule_zeta", "rule_alpha", "rule_mid"} {
		rule := makePromotionRule(name, p)
		if err := repo.CreateRule(ctx, rule); err != nil {
			t.Fatalf("CreateRule(%q): %v", name, err)
		}
	}

	rules, err := repo.ListRules(ctx)
	if err != nil {
		t.Fatalf("ListRules: %v", err)
	}
	if len(rules) != 3 {
		t.Fatalf("ListRules: got %d, want 3", len(rules))
	}
	want := []string{"rule_alpha", "rule_mid", "rule_zeta"}
	for i, w := range want {
		if rules[i].Name != w {
			t.Errorf("ListRules[%d].Name: got %q, want %q", i, rules[i].Name, w)
		}
	}
}

func TestPromotionRepo_ListRules_EmptyReturnsNil(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, promoTables...)
	ctx := context.Background()
	repo := NewPromotionRepo(pool)

	rules, err := repo.ListRules(ctx)
	if err != nil {
		t.Fatalf("ListRules: %v", err)
	}
	if len(rules) != 0 {
		t.Errorf("ListRules on empty: got %d, want 0", len(rules))
	}
}

func TestPromotionRepo_ListRulesByFromRepo_Filters(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, promoTables...)
	ctx := context.Background()
	repo := NewPromotionRepo(pool)

	// Two distinct parent sets so from_repo names differ.
	pA := makePromotionParents(t, ctx, "lrf_a")
	pB := makePromotionParents(t, ctx, "lrf_b")

	// Two rules out of pA, one out of pB.
	for _, name := range []string{"rule_from_a1", "rule_from_a2"} {
		if err := repo.CreateRule(ctx, makePromotionRule(name, pA)); err != nil {
			t.Fatalf("CreateRule(%q): %v", name, err)
		}
	}
	if err := repo.CreateRule(ctx, makePromotionRule("rule_from_b1", pB)); err != nil {
		t.Fatalf("CreateRule(b1): %v", err)
	}

	aRules, err := repo.ListRulesByFromRepo(ctx, pA.FromRepo)
	if err != nil {
		t.Fatalf("ListRulesByFromRepo(A): %v", err)
	}
	if len(aRules) != 2 {
		t.Fatalf("ListRulesByFromRepo(A): got %d, want 2", len(aRules))
	}
	for _, r := range aRules {
		if r.FromRepo != pA.FromRepo {
			t.Errorf("from_repo: got %q, want %q", r.FromRepo, pA.FromRepo)
		}
	}
	// Ordered by name within the filter.
	if aRules[0].Name != "rule_from_a1" || aRules[1].Name != "rule_from_a2" {
		t.Errorf("ListRulesByFromRepo(A) order: got %q,%q", aRules[0].Name, aRules[1].Name)
	}

	bRules, err := repo.ListRulesByFromRepo(ctx, pB.FromRepo)
	if err != nil {
		t.Fatalf("ListRulesByFromRepo(B): %v", err)
	}
	if len(bRules) != 1 {
		t.Errorf("ListRulesByFromRepo(B): got %d, want 1", len(bRules))
	}
}

func TestPromotionRepo_ListRulesByFromRepo_NoMatchReturnsEmpty(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, promoTables...)
	ctx := context.Background()
	repo := NewPromotionRepo(pool)

	rules, err := repo.ListRulesByFromRepo(ctx, "no-such-repo")
	if err != nil {
		t.Fatalf("ListRulesByFromRepo: %v", err)
	}
	if len(rules) != 0 {
		t.Errorf("got %d rules, want 0", len(rules))
	}
}

// ── Rules: Update ─────────────────────────────────────────────────────────────

func TestPromotionRepo_UpdateRule_PersistsAllFields(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, promoTables...)
	ctx := context.Background()
	repo := NewPromotionRepo(pool)

	p := makePromotionParents(t, ctx, "ur_all")
	rule := makePromotionRule("rule_ur_all", p)
	if err := repo.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	// Flip every mutable field.
	rule.Name = "rule_ur_all_renamed"
	rule.FromRepo = p.ToRepo // swap from/to (both valid repo names)
	rule.ToRepo = p.FromRepo
	rule.PathFilter = `path == "snapshot.txt"`
	rule.RequireScanPass = false
	rule.RequireManualApproval = false
	if err := repo.UpdateRule(ctx, rule); err != nil {
		t.Fatalf("UpdateRule: %v", err)
	}

	got, err := repo.GetRule(ctx, rule.ID)
	if err != nil {
		t.Fatalf("GetRule: %v", err)
	}
	if got == nil {
		t.Fatal("GetRule returned nil")
	}
	if got.Name != "rule_ur_all_renamed" {
		t.Errorf("Name: got %q, want rule_ur_all_renamed", got.Name)
	}
	if got.FromRepo != p.ToRepo {
		t.Errorf("FromRepo: got %q, want %q", got.FromRepo, p.ToRepo)
	}
	if got.ToRepo != p.FromRepo {
		t.Errorf("ToRepo: got %q, want %q", got.ToRepo, p.FromRepo)
	}
	if got.PathFilter != `path == "snapshot.txt"` {
		t.Errorf("PathFilter: got %q", got.PathFilter)
	}
	if got.RequireScanPass {
		t.Error("RequireScanPass: got true, want false")
	}
	if got.RequireManualApproval {
		t.Error("RequireManualApproval: got true, want false")
	}
}

func TestPromotionRepo_UpdateRule_ClearPathFilter(t *testing.T) {
	// Updating PathFilter to "" must clear it (NULL) so it round-trips empty.
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, promoTables...)
	ctx := context.Background()
	repo := NewPromotionRepo(pool)

	p := makePromotionParents(t, ctx, "ur_clearpf")
	rule := makePromotionRule("rule_ur_clearpf", p)
	if err := repo.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	rule.PathFilter = ""
	if err := repo.UpdateRule(ctx, rule); err != nil {
		t.Fatalf("UpdateRule: %v", err)
	}
	got, err := repo.GetRule(ctx, rule.ID)
	if err != nil {
		t.Fatalf("GetRule: %v", err)
	}
	if got.PathFilter != "" {
		t.Errorf("PathFilter: got %q, want empty", got.PathFilter)
	}
}

func TestPromotionRepo_UpdateRule_NonexistentIsNoOp(t *testing.T) {
	// UPDATE matching zero rows returns nil (no error) per the repo's contract.
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, promoTables...)
	ctx := context.Background()
	repo := NewPromotionRepo(pool)

	p := makePromotionParents(t, ctx, "ur_noexist")
	rule := makePromotionRule("rule_ur_noexist", p)
	rule.ID = promoZeroUUID
	if err := repo.UpdateRule(ctx, rule); err != nil {
		t.Fatalf("UpdateRule(missing): got err %v, want nil", err)
	}
	// Nothing should have been inserted.
	got, err := repo.GetRule(ctx, promoZeroUUID)
	if err != nil {
		t.Fatalf("GetRule: %v", err)
	}
	if got != nil {
		t.Errorf("GetRule(missing after no-op update): got %+v, want nil", got)
	}
}

// ── Rules: Delete ─────────────────────────────────────────────────────────────

func TestPromotionRepo_DeleteRule_RemovesRow(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, promoTables...)
	ctx := context.Background()
	repo := NewPromotionRepo(pool)

	p := makePromotionParents(t, ctx, "dr_ok")
	rule := makePromotionRule("rule_dr_ok", p)
	if err := repo.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	if err := repo.DeleteRule(ctx, rule.ID); err != nil {
		t.Fatalf("DeleteRule: %v", err)
	}
	got, err := repo.GetRule(ctx, rule.ID)
	if err != nil {
		t.Fatalf("GetRule after delete: %v", err)
	}
	if got != nil {
		t.Errorf("GetRule after delete: got %+v, want nil", got)
	}
}

func TestPromotionRepo_DeleteRule_NonexistentIsNoOp(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, promoTables...)
	ctx := context.Background()
	repo := NewPromotionRepo(pool)

	if err := repo.DeleteRule(ctx, promoZeroUUID); err != nil {
		t.Fatalf("DeleteRule(missing): got err %v, want nil", err)
	}
}

// ── Requests: Create ──────────────────────────────────────────────────────────

func TestPromotionRepo_CreateRequest_PopulatesIDAndTimestamp(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, promoTables...)
	ctx := context.Background()
	repo := NewPromotionRepo(pool)

	p := makePromotionParents(t, ctx, "creq_ts")
	rule := makePromotionRule("rule_creq_ts", p)
	if err := repo.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	compID := makePromotionComponent(t, ctx, p.FromRepoID, "1.0.0")

	req := &domain.PromotionRequest{
		RuleID:      rule.ID,
		ComponentID: compID,
		Status:      domain.PromotionPending,
		RequestedBy: p.UserID,
	}
	if err := repo.CreateRequest(ctx, req); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	if req.ID == "" {
		t.Fatal("CreateRequest did not populate ID")
	}
	if req.CreatedAt.IsZero() {
		t.Error("CreateRequest did not populate CreatedAt")
	}
}

func TestPromotionRepo_CreateRequest_FieldsRoundTrip(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, promoTables...)
	ctx := context.Background()
	repo := NewPromotionRepo(pool)

	p := makePromotionParents(t, ctx, "creq_rt")
	rule := makePromotionRule("rule_creq_rt", p)
	if err := repo.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	compID := makePromotionComponent(t, ctx, p.FromRepoID, "2.0.0")

	req := &domain.PromotionRequest{
		RuleID:      rule.ID,
		ComponentID: compID,
		Status:      domain.PromotionPending,
		RequestedBy: p.UserID,
	}
	if err := repo.CreateRequest(ctx, req); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}

	got, err := repo.GetRequest(ctx, req.ID)
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if got == nil {
		t.Fatal("GetRequest returned nil")
	}
	if got.RuleID != rule.ID {
		t.Errorf("RuleID: got %q, want %q", got.RuleID, rule.ID)
	}
	if got.ComponentID != compID {
		t.Errorf("ComponentID: got %q, want %q", got.ComponentID, compID)
	}
	if got.Status != domain.PromotionPending {
		t.Errorf("Status: got %q, want %q", got.Status, domain.PromotionPending)
	}
	if got.RequestedBy != p.UserID {
		t.Errorf("RequestedBy: got %q, want %q", got.RequestedBy, p.UserID)
	}
	// Freshly-created request: review/completion metadata is nil/empty.
	if got.ReviewedBy != nil {
		t.Errorf("ReviewedBy: got %v, want nil", got.ReviewedBy)
	}
	if got.ReviewedAt != nil {
		t.Errorf("ReviewedAt: got %v, want nil", got.ReviewedAt)
	}
	if got.CompletedAt != nil {
		t.Errorf("CompletedAt: got %v, want nil", got.CompletedAt)
	}
	if got.Error != "" {
		t.Errorf("Error: got %q, want empty", got.Error)
	}
}

// ── Requests: Get ─────────────────────────────────────────────────────────────

func TestPromotionRepo_GetRequest_NotFound_ReturnsNilNil(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, promoTables...)
	ctx := context.Background()
	repo := NewPromotionRepo(pool)

	got, err := repo.GetRequest(ctx, promoZeroUUID)
	if err != nil {
		t.Fatalf("GetRequest(missing): got err %v, want nil", err)
	}
	if got != nil {
		t.Errorf("GetRequest(missing): got %+v, want nil", got)
	}
}

// ── Requests: List + status filter ────────────────────────────────────────────

func TestPromotionRepo_ListRequests_FilterByStatus(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, promoTables...)
	ctx := context.Background()
	repo := NewPromotionRepo(pool)

	p := makePromotionParents(t, ctx, "lreq_filter")
	rule := makePromotionRule("rule_lreq_filter", p)
	if err := repo.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	// 2 pending, 1 approved.
	statuses := []domain.PromotionStatus{
		domain.PromotionPending, domain.PromotionPending, domain.PromotionApproved,
	}
	for i, st := range statuses {
		compID := makePromotionComponent(t, ctx, p.FromRepoID, "v"+time.Duration(i).String())
		req := &domain.PromotionRequest{
			RuleID:      rule.ID,
			ComponentID: compID,
			Status:      st,
			RequestedBy: p.UserID,
		}
		if err := repo.CreateRequest(ctx, req); err != nil {
			t.Fatalf("CreateRequest[%d]: %v", i, err)
		}
	}

	// "" => all 3.
	all, err := repo.ListRequests(ctx, "")
	if err != nil {
		t.Fatalf("ListRequests(all): %v", err)
	}
	if len(all) != 3 {
		t.Errorf("ListRequests(all): got %d, want 3", len(all))
	}

	pending, err := repo.ListRequests(ctx, string(domain.PromotionPending))
	if err != nil {
		t.Fatalf("ListRequests(pending): %v", err)
	}
	if len(pending) != 2 {
		t.Errorf("ListRequests(pending): got %d, want 2", len(pending))
	}
	for _, r := range pending {
		if r.Status != domain.PromotionPending {
			t.Errorf("status: got %q, want pending", r.Status)
		}
	}

	approved, err := repo.ListRequests(ctx, string(domain.PromotionApproved))
	if err != nil {
		t.Fatalf("ListRequests(approved): %v", err)
	}
	if len(approved) != 1 {
		t.Errorf("ListRequests(approved): got %d, want 1", len(approved))
	}
}

func TestPromotionRepo_ListRequests_OrderedNewestFirst(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, promoTables...)
	ctx := context.Background()
	repo := NewPromotionRepo(pool)

	p := makePromotionParents(t, ctx, "lreq_order")
	rule := makePromotionRule("rule_lreq_order", p)
	if err := repo.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	for i := 0; i < 3; i++ {
		compID := makePromotionComponent(t, ctx, p.FromRepoID, "ord-"+time.Duration(i).String())
		req := &domain.PromotionRequest{
			RuleID:      rule.ID,
			ComponentID: compID,
			Status:      domain.PromotionPending,
			RequestedBy: p.UserID,
		}
		if err := repo.CreateRequest(ctx, req); err != nil {
			t.Fatalf("CreateRequest[%d]: %v", i, err)
		}
	}

	reqs, err := repo.ListRequests(ctx, "")
	if err != nil {
		t.Fatalf("ListRequests: %v", err)
	}
	if len(reqs) != 3 {
		t.Fatalf("ListRequests: got %d, want 3", len(reqs))
	}
	// created_at DESC.
	for i := 1; i < len(reqs); i++ {
		if reqs[i].CreatedAt.After(reqs[i-1].CreatedAt) {
			t.Errorf("not DESC at index %d: %v > %v", i, reqs[i].CreatedAt, reqs[i-1].CreatedAt)
		}
	}
}

func TestPromotionRepo_ListRequests_EmptyReturnsNil(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, promoTables...)
	ctx := context.Background()
	repo := NewPromotionRepo(pool)

	reqs, err := repo.ListRequests(ctx, "")
	if err != nil {
		t.Fatalf("ListRequests: %v", err)
	}
	if len(reqs) != 0 {
		t.Errorf("ListRequests on empty: got %d, want 0", len(reqs))
	}
}

// ── Requests: status transitions ──────────────────────────────────────────────

func TestPromotionRepo_UpdateRequestStatus_Approve(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, promoTables...)
	ctx := context.Background()
	repo := NewPromotionRepo(pool)

	p := makePromotionParents(t, ctx, "urs_approve")
	rule := makePromotionRule("rule_urs_approve", p)
	if err := repo.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	compID := makePromotionComponent(t, ctx, p.FromRepoID, "1.0.0")

	req := &domain.PromotionRequest{
		RuleID:      rule.ID,
		ComponentID: compID,
		Status:      domain.PromotionPending,
		RequestedBy: p.UserID,
	}
	if err := repo.CreateRequest(ctx, req); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}

	reviewedBy := p.UserID
	reviewedAt := time.Now().Truncate(time.Second)
	if err := repo.UpdateRequestStatus(
		ctx, req.ID, domain.PromotionApproved,
		&reviewedBy, &reviewedAt, nil, "",
	); err != nil {
		t.Fatalf("UpdateRequestStatus(approve): %v", err)
	}

	got, err := repo.GetRequest(ctx, req.ID)
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if got.Status != domain.PromotionApproved {
		t.Errorf("Status: got %q, want approved", got.Status)
	}
	if got.ReviewedBy == nil || *got.ReviewedBy != reviewedBy {
		t.Errorf("ReviewedBy: got %v, want %q", got.ReviewedBy, reviewedBy)
	}
	if got.ReviewedAt == nil {
		t.Error("ReviewedAt: got nil, want non-nil")
	} else if !got.ReviewedAt.Equal(reviewedAt) {
		t.Errorf("ReviewedAt: got %v, want %v", got.ReviewedAt, reviewedAt)
	}
	if got.CompletedAt != nil {
		t.Errorf("CompletedAt: got %v, want nil", got.CompletedAt)
	}
	if got.Error != "" {
		t.Errorf("Error: got %q, want empty", got.Error)
	}
}

func TestPromotionRepo_UpdateRequestStatus_Reject(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, promoTables...)
	ctx := context.Background()
	repo := NewPromotionRepo(pool)

	p := makePromotionParents(t, ctx, "urs_reject")
	rule := makePromotionRule("rule_urs_reject", p)
	if err := repo.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	compID := makePromotionComponent(t, ctx, p.FromRepoID, "1.0.0")

	req := &domain.PromotionRequest{
		RuleID:      rule.ID,
		ComponentID: compID,
		Status:      domain.PromotionPending,
		RequestedBy: p.UserID,
	}
	if err := repo.CreateRequest(ctx, req); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}

	reviewedBy := p.UserID
	reviewedAt := time.Now().Truncate(time.Second)
	if err := repo.UpdateRequestStatus(
		ctx, req.ID, domain.PromotionRejected,
		&reviewedBy, &reviewedAt, nil, "not approved by policy",
	); err != nil {
		t.Fatalf("UpdateRequestStatus(reject): %v", err)
	}

	got, err := repo.GetRequest(ctx, req.ID)
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if got.Status != domain.PromotionRejected {
		t.Errorf("Status: got %q, want rejected", got.Status)
	}
	if got.ReviewedBy == nil || *got.ReviewedBy != reviewedBy {
		t.Errorf("ReviewedBy: got %v, want %q", got.ReviewedBy, reviewedBy)
	}
	if got.Error != "not approved by policy" {
		t.Errorf("Error: got %q, want 'not approved by policy'", got.Error)
	}
}

func TestPromotionRepo_UpdateRequestStatus_Complete(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, promoTables...)
	ctx := context.Background()
	repo := NewPromotionRepo(pool)

	p := makePromotionParents(t, ctx, "urs_complete")
	rule := makePromotionRule("rule_urs_complete", p)
	if err := repo.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	compID := makePromotionComponent(t, ctx, p.FromRepoID, "1.0.0")

	req := &domain.PromotionRequest{
		RuleID:      rule.ID,
		ComponentID: compID,
		Status:      domain.PromotionApproved,
		RequestedBy: p.UserID,
	}
	if err := repo.CreateRequest(ctx, req); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}

	completedAt := time.Now().Truncate(time.Second)
	if err := repo.UpdateRequestStatus(
		ctx, req.ID, domain.PromotionCompleted,
		nil, nil, &completedAt, "",
	); err != nil {
		t.Fatalf("UpdateRequestStatus(complete): %v", err)
	}

	got, err := repo.GetRequest(ctx, req.ID)
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if got.Status != domain.PromotionCompleted {
		t.Errorf("Status: got %q, want completed", got.Status)
	}
	if got.CompletedAt == nil {
		t.Error("CompletedAt: got nil, want non-nil")
	} else if !got.CompletedAt.Equal(completedAt) {
		t.Errorf("CompletedAt: got %v, want %v", got.CompletedAt, completedAt)
	}
	// reviewedBy/reviewedAt were passed nil — must remain nil.
	if got.ReviewedBy != nil {
		t.Errorf("ReviewedBy: got %v, want nil", got.ReviewedBy)
	}
	if got.ReviewedAt != nil {
		t.Errorf("ReviewedAt: got %v, want nil", got.ReviewedAt)
	}
}

func TestPromotionRepo_UpdateRequestStatus_Failed(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, promoTables...)
	ctx := context.Background()
	repo := NewPromotionRepo(pool)

	p := makePromotionParents(t, ctx, "urs_failed")
	rule := makePromotionRule("rule_urs_failed", p)
	if err := repo.CreateRule(ctx, rule); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	compID := makePromotionComponent(t, ctx, p.FromRepoID, "1.0.0")

	req := &domain.PromotionRequest{
		RuleID:      rule.ID,
		ComponentID: compID,
		Status:      domain.PromotionApproved,
		RequestedBy: p.UserID,
	}
	if err := repo.CreateRequest(ctx, req); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}

	completedAt := time.Now().Truncate(time.Second)
	if err := repo.UpdateRequestStatus(
		ctx, req.ID, domain.PromotionFailed,
		nil, nil, &completedAt, "blob copy failed: disk full",
	); err != nil {
		t.Fatalf("UpdateRequestStatus(failed): %v", err)
	}

	got, err := repo.GetRequest(ctx, req.ID)
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if got.Status != domain.PromotionFailed {
		t.Errorf("Status: got %q, want failed", got.Status)
	}
	if got.Error != "blob copy failed: disk full" {
		t.Errorf("Error: got %q", got.Error)
	}
}

func TestPromotionRepo_UpdateRequestStatus_NonexistentIsNoOp(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, promoTables...)
	ctx := context.Background()
	repo := NewPromotionRepo(pool)

	if err := repo.UpdateRequestStatus(
		ctx, promoZeroUUID, domain.PromotionApproved,
		nil, nil, nil, "",
	); err != nil {
		t.Fatalf("UpdateRequestStatus(missing): got err %v, want nil", err)
	}
	got, err := repo.GetRequest(ctx, promoZeroUUID)
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if got != nil {
		t.Errorf("GetRequest(missing): got %+v, want nil", got)
	}
}
