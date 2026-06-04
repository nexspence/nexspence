//go:build integration

package postgres

import (
	"context"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/testutil/pgtest"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// newCS returns a fresh ContentSelector value (not inserted).
func newCS(name, description, expression string) *domain.ContentSelector {
	return &domain.ContentSelector{
		Name:        name,
		Description: description,
		Expression:  expression,
	}
}

// insertCS creates a content selector and fatals on error.
func insertCS(t *testing.T, ctx context.Context, repo *ContentSelectorRepo, cs *domain.ContentSelector) {
	t.Helper()
	if err := repo.Create(ctx, cs); err != nil {
		t.Fatalf("insertCS %q: %v", cs.Name, err)
	}
}

// ── Create ────────────────────────────────────────────────────────────────────

func TestContentSelectorRepo_Create_PopulatesIDAndTimestamps(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	repo := NewContentSelectorRepo(pool)

	cs := newCS("create_ts_cs2", "a description", `path.startsWith("/maven2/")`)
	if err := repo.Create(ctx, cs); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if cs.ID == "" {
		t.Fatal("Create did not populate ID")
	}
	if cs.CreatedAt.IsZero() {
		t.Fatal("Create did not populate CreatedAt")
	}
	if cs.UpdatedAt.IsZero() {
		t.Fatal("Create did not populate UpdatedAt")
	}
}

func TestContentSelectorRepo_Create_FieldsRoundTrip(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	repo := NewContentSelectorRepo(pool)

	cs := newCS("roundtrip_cs2", "round-trip description", `format == "npm" && path.startsWith("/dist/")`)
	insertCS(t, ctx, repo, cs)

	got, err := repo.Get(ctx, cs.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.ID != cs.ID {
		t.Errorf("ID: got %q, want %q", got.ID, cs.ID)
	}
	if got.Name != cs.Name {
		t.Errorf("Name: got %q, want %q", got.Name, cs.Name)
	}
	if got.Description != cs.Description {
		t.Errorf("Description: got %q, want %q", got.Description, cs.Description)
	}
	if got.Expression != cs.Expression {
		t.Errorf("Expression: got %q, want %q", got.Expression, cs.Expression)
	}
	if got.CreatedAt.IsZero() {
		t.Error("CreatedAt is zero")
	}
	if got.UpdatedAt.IsZero() {
		t.Error("UpdatedAt is zero")
	}
}

func TestContentSelectorRepo_Create_EmptyDescription(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	repo := NewContentSelectorRepo(pool)

	cs := newCS("empty_desc_cs", "", `true`)
	insertCS(t, ctx, repo, cs)

	got, err := repo.Get(ctx, cs.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.Description != "" {
		t.Errorf("Description: got %q, want empty", got.Description)
	}
}

func TestContentSelectorRepo_Create_DuplicateName_Errors(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	repo := NewContentSelectorRepo(pool)

	cs1 := newCS("dup_cs_name", "", `true`)
	insertCS(t, ctx, repo, cs1)

	cs2 := newCS("dup_cs_name", "other", `false`)
	if err := repo.Create(ctx, cs2); err == nil {
		t.Fatal("expected error for duplicate name, got nil")
	}
}

// ── Get ───────────────────────────────────────────────────────────────────────

func TestContentSelectorRepo_Get_NotFound_ReturnsNil(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	repo := NewContentSelectorRepo(pool)

	const missing = "00000000-0000-0000-0000-000000000000"
	got, err := repo.Get(ctx, missing)
	if err != nil {
		t.Fatalf("Get(missing): unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("Get(missing): expected nil, got %+v", got)
	}
}

// ── GetByName ─────────────────────────────────────────────────────────────────

func TestContentSelectorRepo_GetByName_HappyPath(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	repo := NewContentSelectorRepo(pool)

	cs := newCS("getbyname_cs2", "desc", `path == "/a"`)
	insertCS(t, ctx, repo, cs)

	got, err := repo.GetByName(ctx, "getbyname_cs2")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if got == nil {
		t.Fatal("GetByName returned nil")
	}
	if got.ID != cs.ID {
		t.Errorf("ID: got %q, want %q", got.ID, cs.ID)
	}
	if got.Expression != `path == "/a"` {
		t.Errorf("Expression: got %q, want %q", got.Expression, `path == "/a"`)
	}
}

func TestContentSelectorRepo_GetByName_NotFound_ReturnsNil(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	repo := NewContentSelectorRepo(pool)

	got, err := repo.GetByName(ctx, "nonexistent_cs_xyz")
	if err != nil {
		t.Fatalf("GetByName(missing): unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("GetByName(missing): expected nil, got %+v", got)
	}
}

// ── Update ────────────────────────────────────────────────────────────────────

func TestContentSelectorRepo_Update_PersistsChanges(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	repo := NewContentSelectorRepo(pool)

	cs := newCS("update_cs", "original desc", `true`)
	insertCS(t, ctx, repo, cs)

	cs.Name = "update_cs_renamed"
	cs.Description = "updated description"
	cs.Expression = `format == "maven2"`
	if err := repo.Update(ctx, cs); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := repo.GetByName(ctx, "update_cs_renamed")
	if err != nil {
		t.Fatalf("GetByName after Update: %v", err)
	}
	if got == nil {
		t.Fatal("GetByName after Update returned nil")
	}
	if got.Description != "updated description" {
		t.Errorf("Description: got %q, want %q", got.Description, "updated description")
	}
	if got.Expression != `format == "maven2"` {
		t.Errorf("Expression: got %q, want %q", got.Expression, `format == "maven2"`)
	}
}

func TestContentSelectorRepo_Update_NotFound_Errors(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	repo := NewContentSelectorRepo(pool)

	cs := &domain.ContentSelector{
		ID:         "00000000-0000-0000-0000-000000000000",
		Name:       "ghost",
		Expression: `true`,
	}
	if err := repo.Update(ctx, cs); err == nil {
		t.Fatal("Update(missing): expected error, got nil")
	}
}

// ── Delete ────────────────────────────────────────────────────────────────────

func TestContentSelectorRepo_Delete_RemovesSelector(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	repo := NewContentSelectorRepo(pool)

	cs := newCS("delete_cs", "", `true`)
	insertCS(t, ctx, repo, cs)

	if err := repo.Delete(ctx, cs.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	got, err := repo.Get(ctx, cs.ID)
	if err != nil {
		t.Fatalf("Get after Delete: %v", err)
	}
	if got != nil {
		t.Fatal("Get after Delete: expected nil, selector still exists")
	}
}

func TestContentSelectorRepo_Delete_UnknownID_NoError(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	repo := NewContentSelectorRepo(pool)

	const missing = "00000000-0000-0000-0000-000000000000"
	if err := repo.Delete(ctx, missing); err != nil {
		t.Fatalf("Delete(missing): unexpected error: %v", err)
	}
}

// ── List ──────────────────────────────────────────────────────────────────────

func TestContentSelectorRepo_List_Empty(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	repo := NewContentSelectorRepo(pool)

	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected empty list, got %d", len(list))
	}
}

func TestContentSelectorRepo_List_OrderedByName(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	repo := NewContentSelectorRepo(pool)

	for _, name := range []string{"zzz_list_cs", "aaa_list_cs", "mmm_list_cs"} {
		cs := newCS(name, "", `true`)
		insertCS(t, ctx, repo, cs)
	}

	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("List: got %d, want 3", len(list))
	}
	if list[0].Name != "aaa_list_cs" || list[1].Name != "mmm_list_cs" || list[2].Name != "zzz_list_cs" {
		t.Errorf("List not ordered: got %q %q %q", list[0].Name, list[1].Name, list[2].Name)
	}
}

func TestContentSelectorRepo_List_ContainsAllCreated(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	repo := NewContentSelectorRepo(pool)

	cs1 := newCS("list_all_cs1", "desc1", `format == "npm"`)
	cs2 := newCS("list_all_cs2", "desc2", `format == "pypi"`)
	insertCS(t, ctx, repo, cs1)
	insertCS(t, ctx, repo, cs2)

	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("List: got %d, want 2", len(list))
	}
	// list[0] is list_all_cs1 (alphabetically first).
	if list[0].Expression != `format == "npm"` {
		t.Errorf("list[0].Expression: got %q, want %q", list[0].Expression, `format == "npm"`)
	}
	if list[1].Expression != `format == "pypi"` {
		t.Errorf("list[1].Expression: got %q, want %q", list[1].Expression, `format == "pypi"`)
	}
}

// ── ListForUser ───────────────────────────────────────────────────────────────

func TestContentSelectorRepo_ListForUser_EmptyUserID_ReturnsNil(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	repo := NewContentSelectorRepo(pool)

	got, err := repo.ListForUser(ctx, "")
	if err != nil {
		t.Fatalf("ListForUser(empty): %v", err)
	}
	if got != nil {
		t.Fatalf("ListForUser(empty): expected nil, got %v", got)
	}
}

func TestContentSelectorRepo_ListForUser_NoRoles_ReturnsEmpty(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "users", "roles", "privileges", "content_selectors")
	ctx := context.Background()
	csRepo := NewContentSelectorRepo(pool)
	userRepo := NewUserRepo(pool)

	u := makeUser("lfu_norolcs_user", "lfu_norolcs@test.com")
	if err := userRepo.Create(ctx, u); err != nil {
		t.Fatalf("Create user: %v", err)
	}

	got, err := csRepo.ListForUser(ctx, u.ID)
	if err != nil {
		t.Fatalf("ListForUser: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty, got %d", len(got))
	}
}

func TestContentSelectorRepo_ListForUser_ViaRoleAndPrivilege(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "users", "roles", "privileges", "content_selectors")
	ctx := context.Background()
	csRepo := NewContentSelectorRepo(pool)
	userRepo := NewUserRepo(pool)
	roleRepo := NewRoleRepo(pool)
	privRepo := NewPrivilegeRepo(pool)

	// Create a content selector.
	cs := newCS("lfu_cs", "for user test", `format == "raw"`)
	insertCS(t, ctx, csRepo, cs)

	// Create a privilege referencing the selector.
	priv := makePriv("lfu_priv", &cs.ID)
	if err := privRepo.Create(ctx, priv); err != nil {
		t.Fatalf("Create priv: %v", err)
	}

	// Create a role and attach the privilege.
	role := makeRole("lfu_role", "local")
	if err := roleRepo.Create(ctx, role); err != nil {
		t.Fatalf("Create role: %v", err)
	}
	if err := roleRepo.SetPrivileges(ctx, role.ID, []string{priv.ID}); err != nil {
		t.Fatalf("SetPrivileges: %v", err)
	}

	// Create a user and assign the role.
	u := makeUser("lfu_main_user", "lfu_main@test.com")
	if err := userRepo.Create(ctx, u); err != nil {
		t.Fatalf("Create user: %v", err)
	}
	if err := roleRepo.SetUserRoles(ctx, u.ID, []string{role.ID}); err != nil {
		t.Fatalf("SetUserRoles: %v", err)
	}

	got, err := csRepo.ListForUser(ctx, u.ID)
	if err != nil {
		t.Fatalf("ListForUser: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListForUser: got %d selectors, want 1", len(got))
	}
	if got[0].ID != cs.ID {
		t.Errorf("ListForUser: got selector %q, want %q", got[0].ID, cs.ID)
	}
	if got[0].Expression != `format == "raw"` {
		t.Errorf("ListForUser expression: got %q, want %q", got[0].Expression, `format == "raw"`)
	}
}

func TestContentSelectorRepo_ListForUser_DISTINCT_NoDuplicates(t *testing.T) {
	// The same selector is attached to two roles, both assigned to the user.
	// DISTINCT should ensure it appears only once.
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "users", "roles", "privileges", "content_selectors")
	ctx := context.Background()
	csRepo := NewContentSelectorRepo(pool)
	userRepo := NewUserRepo(pool)
	roleRepo := NewRoleRepo(pool)
	privRepo := NewPrivilegeRepo(pool)

	cs := newCS("distinct_cs", "", `true`)
	insertCS(t, ctx, csRepo, cs)

	priv := makePriv("distinct_priv", &cs.ID)
	if err := privRepo.Create(ctx, priv); err != nil {
		t.Fatalf("Create priv: %v", err)
	}

	r1 := makeRole("distinct_r1", "local")
	r2 := makeRole("distinct_r2", "local")
	if err := roleRepo.Create(ctx, r1); err != nil {
		t.Fatalf("Create r1: %v", err)
	}
	if err := roleRepo.Create(ctx, r2); err != nil {
		t.Fatalf("Create r2: %v", err)
	}
	if err := roleRepo.SetPrivileges(ctx, r1.ID, []string{priv.ID}); err != nil {
		t.Fatalf("SetPrivileges r1: %v", err)
	}
	if err := roleRepo.SetPrivileges(ctx, r2.ID, []string{priv.ID}); err != nil {
		t.Fatalf("SetPrivileges r2: %v", err)
	}

	u := makeUser("distinct_user", "distinct@test.com")
	if err := userRepo.Create(ctx, u); err != nil {
		t.Fatalf("Create user: %v", err)
	}
	if err := roleRepo.SetUserRoles(ctx, u.ID, []string{r1.ID, r2.ID}); err != nil {
		t.Fatalf("SetUserRoles: %v", err)
	}

	got, err := csRepo.ListForUser(ctx, u.ID)
	if err != nil {
		t.Fatalf("ListForUser: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListForUser DISTINCT: got %d, want 1 (no duplicates)", len(got))
	}
}

// ── AttachToPrivilege / DetachFromPrivilege ───────────────────────────────────

func TestContentSelectorRepo_AttachToPrivilege_HappyPath(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	csRepo := NewContentSelectorRepo(pool)
	privRepo := NewPrivilegeRepo(pool)

	cs1 := newCS("attach_cs1", "", `true`)
	cs2 := newCS("attach_cs2", "", `false`)
	insertCS(t, ctx, csRepo, cs1)
	insertCS(t, ctx, csRepo, cs2)

	// Create privilege with cs1 attached.
	priv := makePriv("attach_priv", &cs1.ID)
	if err := privRepo.Create(ctx, priv); err != nil {
		t.Fatalf("Create priv: %v", err)
	}

	// Reattach to cs2.
	if err := csRepo.AttachToPrivilege(ctx, "attach_priv", cs2.ID); err != nil {
		t.Fatalf("AttachToPrivilege: %v", err)
	}

	got, err := privRepo.GetByName(ctx, "attach_priv")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if got == nil {
		t.Fatal("GetByName returned nil")
	}
	if got.ContentSelectorID == nil || *got.ContentSelectorID != cs2.ID {
		t.Errorf("ContentSelectorID after attach: got %v, want %s", got.ContentSelectorID, cs2.ID)
	}
}

func TestContentSelectorRepo_AttachToPrivilege_PrivilegeNotFound_Errors(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	csRepo := NewContentSelectorRepo(pool)

	cs := newCS("attach_missing_cs", "", `true`)
	insertCS(t, ctx, csRepo, cs)

	if err := csRepo.AttachToPrivilege(ctx, "nonexistent_priv_xyz", cs.ID); err == nil {
		t.Fatal("AttachToPrivilege(missing priv): expected error, got nil")
	}
}

func TestContentSelectorRepo_DetachFromPrivilege_HappyPath(t *testing.T) {
	// DetachFromPrivilege sets content_selector_id = NULL; since migration 007 forbids NULL
	// for repository-content-selector type, we use a wildcard privilege for this test.
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	csRepo := NewContentSelectorRepo(pool)

	cs := newCS("detach_cs", "", `true`)
	insertCS(t, ctx, csRepo, cs)

	// Insert a wildcard privilege (not repository-content-selector) so NULL selector is allowed.
	var privID string
	err := pool.QueryRow(ctx,
		`INSERT INTO privileges (name, description, type, attrs, content_selector_id)
		 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		"detach_priv", "", "wildcard", `{}`, cs.ID,
	).Scan(&privID)
	if err != nil {
		t.Fatalf("Insert wildcard privilege: %v", err)
	}

	if err := csRepo.DetachFromPrivilege(ctx, "detach_priv"); err != nil {
		t.Fatalf("DetachFromPrivilege: %v", err)
	}

	// Verify selector is cleared.
	var selectorID *string
	if err := pool.QueryRow(ctx,
		`SELECT content_selector_id FROM privileges WHERE name = $1`, "detach_priv",
	).Scan(&selectorID); err != nil {
		t.Fatalf("Query after detach: %v", err)
	}
	if selectorID != nil {
		t.Errorf("content_selector_id after detach: got %v, want nil", *selectorID)
	}
}

func TestContentSelectorRepo_DetachFromPrivilege_PrivilegeNotFound_Errors(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	repo := NewContentSelectorRepo(pool)

	if err := repo.DetachFromPrivilege(ctx, "nonexistent_priv_xyz"); err == nil {
		t.Fatal("DetachFromPrivilege(missing priv): expected error, got nil")
	}
}
