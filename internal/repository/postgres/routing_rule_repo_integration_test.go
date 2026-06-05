//go:build integration

package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/testutil/pgtest"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// newRR returns a fresh RoutingRule value (not inserted).
func newRR(name, description, mode string, matchers []string) *domain.RoutingRule {
	return &domain.RoutingRule{
		Name:        name,
		Description: description,
		Mode:        mode,
		Matchers:    matchers,
	}
}

// insertRR inserts a routing rule and fatals on error.
func insertRR(t *testing.T, ctx context.Context, repo *RoutingRuleRepo, rr *domain.RoutingRule) {
	t.Helper()
	if err := repo.Create(ctx, rr); err != nil {
		t.Fatalf("insertRR %q: %v", rr.Name, err)
	}
}

// ── Create ────────────────────────────────────────────────────────────────────

func TestRoutingRuleRepo_Create_PopulatesIDAndTimestamps(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "routing_rules")
	ctx := context.Background()
	repo := NewRoutingRuleRepo(pool)

	rr := newRR("create_rr_ts", "desc", "BLOCK", []string{`^/private/.*`})
	if err := repo.Create(ctx, rr); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if rr.ID == "" {
		t.Fatal("Create did not populate ID")
	}
	if rr.CreatedAt.IsZero() {
		t.Fatal("Create did not populate CreatedAt")
	}
	if rr.UpdatedAt.IsZero() {
		t.Fatal("Create did not populate UpdatedAt")
	}
}

func TestRoutingRuleRepo_Create_FieldsRoundTrip_BLOCK(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "routing_rules")
	ctx := context.Background()
	repo := NewRoutingRuleRepo(pool)

	matchers := []string{`^/secret/.*`, `^/private/.*`}
	rr := newRR("roundtrip_block_rr", "round-trip block", "BLOCK", matchers)
	insertRR(t, ctx, repo, rr)

	got, err := repo.Get(ctx, rr.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.ID != rr.ID {
		t.Errorf("ID: got %q, want %q", got.ID, rr.ID)
	}
	if got.Name != rr.Name {
		t.Errorf("Name: got %q, want %q", got.Name, rr.Name)
	}
	if got.Description != rr.Description {
		t.Errorf("Description: got %q, want %q", got.Description, rr.Description)
	}
	if got.Mode != "BLOCK" {
		t.Errorf("Mode: got %q, want BLOCK", got.Mode)
	}
	if len(got.Matchers) != 2 {
		t.Fatalf("Matchers: got %d, want 2", len(got.Matchers))
	}
	if got.Matchers[0] != matchers[0] || got.Matchers[1] != matchers[1] {
		t.Errorf("Matchers: got %v, want %v", got.Matchers, matchers)
	}
}

func TestRoutingRuleRepo_Create_FieldsRoundTrip_ALLOW(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "routing_rules")
	ctx := context.Background()
	repo := NewRoutingRuleRepo(pool)

	rr := newRR("roundtrip_allow_rr", "", "ALLOW", []string{`^/public/.*`})
	insertRR(t, ctx, repo, rr)

	got, err := repo.Get(ctx, rr.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.Mode != "ALLOW" {
		t.Errorf("Mode: got %q, want ALLOW", got.Mode)
	}
}

func TestRoutingRuleRepo_Create_EmptyMatchers(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "routing_rules")
	ctx := context.Background()
	repo := NewRoutingRuleRepo(pool)

	rr := newRR("empty_matchers_rr", "", "BLOCK", []string{})
	insertRR(t, ctx, repo, rr)

	got, err := repo.Get(ctx, rr.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if len(got.Matchers) != 0 {
		t.Errorf("Matchers: got %v, want empty", got.Matchers)
	}
}

func TestRoutingRuleRepo_Create_DuplicateName_Errors(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "routing_rules")
	ctx := context.Background()
	repo := NewRoutingRuleRepo(pool)

	rr1 := newRR("dup_rr_name", "", "BLOCK", []string{})
	insertRR(t, ctx, repo, rr1)

	rr2 := newRR("dup_rr_name", "other", "ALLOW", []string{})
	if err := repo.Create(ctx, rr2); err == nil {
		t.Fatal("expected error for duplicate name, got nil")
	}
}

func TestRoutingRuleRepo_Create_InvalidMode_Errors(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "routing_rules")
	ctx := context.Background()
	repo := NewRoutingRuleRepo(pool)

	rr := newRR("badmode_rr", "", "PERMIT", []string{})
	if err := repo.Create(ctx, rr); err == nil {
		t.Fatal("Create with invalid mode: expected error, got nil")
	}
}

// ── Get ───────────────────────────────────────────────────────────────────────

func TestRoutingRuleRepo_Get_NotFound_ReturnsNil(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "routing_rules")
	ctx := context.Background()
	repo := NewRoutingRuleRepo(pool)

	const missing = "00000000-0000-0000-0000-000000000000"
	got, err := repo.Get(ctx, missing)
	if !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("Get(missing): want ErrNotFound, got %v", err)
	}
	if got != nil {
		t.Fatalf("Get(missing): expected nil, got %+v", got)
	}
}

// ── GetByName ─────────────────────────────────────────────────────────────────

func TestRoutingRuleRepo_GetByName_HappyPath(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "routing_rules")
	ctx := context.Background()
	repo := NewRoutingRuleRepo(pool)

	rr := newRR("getbyname_rr", "desc", "ALLOW", []string{`^/allowed/.*`})
	insertRR(t, ctx, repo, rr)

	got, err := repo.GetByName(ctx, "getbyname_rr")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if got == nil {
		t.Fatal("GetByName returned nil")
	}
	if got.ID != rr.ID {
		t.Errorf("ID: got %q, want %q", got.ID, rr.ID)
	}
	if got.Mode != "ALLOW" {
		t.Errorf("Mode: got %q, want ALLOW", got.Mode)
	}
	if len(got.Matchers) != 1 || got.Matchers[0] != `^/allowed/.*` {
		t.Errorf("Matchers: got %v, want [^/allowed/.*]", got.Matchers)
	}
}

func TestRoutingRuleRepo_GetByName_NotFound_ReturnsNil(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "routing_rules")
	ctx := context.Background()
	repo := NewRoutingRuleRepo(pool)

	got, err := repo.GetByName(ctx, "nonexistent_rr_xyz")
	if !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("GetByName(missing): want ErrNotFound, got %v", err)
	}
	if got != nil {
		t.Fatalf("GetByName(missing): expected nil, got %+v", got)
	}
}

// ── Update ────────────────────────────────────────────────────────────────────

func TestRoutingRuleRepo_Update_PersistsChanges(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "routing_rules")
	ctx := context.Background()
	repo := NewRoutingRuleRepo(pool)

	rr := newRR("update_rr", "original", "BLOCK", []string{`^/old/.*`})
	insertRR(t, ctx, repo, rr)

	rr.Name = "update_rr_renamed"
	rr.Description = "updated description"
	rr.Mode = "ALLOW"
	rr.Matchers = []string{`^/new/.*`, `^/also/.*`}
	if err := repo.Update(ctx, rr); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := repo.GetByName(ctx, "update_rr_renamed")
	if err != nil {
		t.Fatalf("GetByName after Update: %v", err)
	}
	if got == nil {
		t.Fatal("GetByName after Update returned nil")
	}
	if got.Description != "updated description" {
		t.Errorf("Description: got %q, want %q", got.Description, "updated description")
	}
	if got.Mode != "ALLOW" {
		t.Errorf("Mode: got %q, want ALLOW", got.Mode)
	}
	if len(got.Matchers) != 2 {
		t.Fatalf("Matchers len: got %d, want 2", len(got.Matchers))
	}
	if got.Matchers[0] != `^/new/.*` || got.Matchers[1] != `^/also/.*` {
		t.Errorf("Matchers: got %v", got.Matchers)
	}
}

func TestRoutingRuleRepo_Update_ClearMatchers(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "routing_rules")
	ctx := context.Background()
	repo := NewRoutingRuleRepo(pool)

	rr := newRR("clear_matchers_rr", "", "BLOCK", []string{`^/x/.*`})
	insertRR(t, ctx, repo, rr)

	rr.Matchers = []string{}
	if err := repo.Update(ctx, rr); err != nil {
		t.Fatalf("Update (clear matchers): %v", err)
	}

	got, err := repo.Get(ctx, rr.ID)
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}
	if got == nil {
		t.Fatal("Get after update returned nil")
	}
	if len(got.Matchers) != 0 {
		t.Errorf("Matchers after clear: got %v, want empty", got.Matchers)
	}
}

func TestRoutingRuleRepo_Update_NotFound_Errors(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "routing_rules")
	ctx := context.Background()
	repo := NewRoutingRuleRepo(pool)

	rr := &domain.RoutingRule{
		ID:       "00000000-0000-0000-0000-000000000000",
		Name:     "ghost_rr",
		Mode:     "BLOCK",
		Matchers: []string{},
	}
	if err := repo.Update(ctx, rr); err == nil {
		t.Fatal("Update(missing): expected error, got nil")
	}
}

// ── Delete ────────────────────────────────────────────────────────────────────

func TestRoutingRuleRepo_Delete_RemovesRule(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "routing_rules")
	ctx := context.Background()
	repo := NewRoutingRuleRepo(pool)

	rr := newRR("delete_rr", "", "BLOCK", []string{})
	insertRR(t, ctx, repo, rr)

	if err := repo.Delete(ctx, rr.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	got, err := repo.Get(ctx, rr.ID)
	if !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("Get after Delete: want ErrNotFound, got %v", err)
	}
	if got != nil {
		t.Fatal("Get after Delete: expected nil, rule still exists")
	}
}

func TestRoutingRuleRepo_Delete_UnknownID_NoError(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "routing_rules")
	ctx := context.Background()
	repo := NewRoutingRuleRepo(pool)

	const missing = "00000000-0000-0000-0000-000000000000"
	if err := repo.Delete(ctx, missing); err != nil {
		t.Fatalf("Delete(missing): unexpected error: %v", err)
	}
}

// ── List ──────────────────────────────────────────────────────────────────────

func TestRoutingRuleRepo_List_Empty(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "routing_rules")
	ctx := context.Background()
	repo := NewRoutingRuleRepo(pool)

	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected empty list, got %d", len(list))
	}
}

func TestRoutingRuleRepo_List_OrderedByName(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "routing_rules")
	ctx := context.Background()
	repo := NewRoutingRuleRepo(pool)

	for _, name := range []string{"zzz_list_rr", "aaa_list_rr", "mmm_list_rr"} {
		rr := newRR(name, "", "BLOCK", []string{})
		insertRR(t, ctx, repo, rr)
	}

	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("List: got %d, want 3", len(list))
	}
	if list[0].Name != "aaa_list_rr" || list[1].Name != "mmm_list_rr" || list[2].Name != "zzz_list_rr" {
		t.Errorf("List not ordered: got %q %q %q", list[0].Name, list[1].Name, list[2].Name)
	}
}

func TestRoutingRuleRepo_List_ContainsAllCreated(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "routing_rules")
	ctx := context.Background()
	repo := NewRoutingRuleRepo(pool)

	rr1 := newRR("list_all_rr1", "", "ALLOW", []string{`^/a/.*`})
	rr2 := newRR("list_all_rr2", "", "BLOCK", []string{`^/b/.*`})
	insertRR(t, ctx, repo, rr1)
	insertRR(t, ctx, repo, rr2)

	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("List: got %d, want 2", len(list))
	}
	// Alphabetically ordered: list_all_rr1, list_all_rr2.
	if list[0].Mode != "ALLOW" {
		t.Errorf("list[0].Mode: got %q, want ALLOW", list[0].Mode)
	}
	if list[1].Mode != "BLOCK" {
		t.Errorf("list[1].Mode: got %q, want BLOCK", list[1].Mode)
	}
	if len(list[0].Matchers) != 1 || list[0].Matchers[0] != `^/a/.*` {
		t.Errorf("list[0].Matchers: got %v", list[0].Matchers)
	}
}

func TestRoutingRuleRepo_List_AllFieldsPresent(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "routing_rules")
	ctx := context.Background()
	repo := NewRoutingRuleRepo(pool)

	rr := newRR("fields_check_rr", "my description", "ALLOW", []string{`^/x/.*`, `^/y/.*`})
	insertRR(t, ctx, repo, rr)

	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List: got %d, want 1", len(list))
	}
	got := list[0]
	if got.ID == "" {
		t.Error("ID is empty")
	}
	if got.Name != "fields_check_rr" {
		t.Errorf("Name: got %q, want fields_check_rr", got.Name)
	}
	if got.Description != "my description" {
		t.Errorf("Description: got %q, want my description", got.Description)
	}
	if got.Mode != "ALLOW" {
		t.Errorf("Mode: got %q, want ALLOW", got.Mode)
	}
	if len(got.Matchers) != 2 {
		t.Fatalf("Matchers len: got %d, want 2", len(got.Matchers))
	}
	if got.CreatedAt.IsZero() {
		t.Error("CreatedAt is zero")
	}
	if got.UpdatedAt.IsZero() {
		t.Error("UpdatedAt is zero")
	}
}
