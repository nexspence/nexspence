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

// makeCS inserts a content_selector and returns it. Fatals on error.
func makeCS(t *testing.T, ctx context.Context, name, expr string) *domain.ContentSelector {
	t.Helper()
	pool := pgtest.Pool(t)
	csRepo := NewContentSelectorRepo(pool)
	cs := &domain.ContentSelector{Name: name, Description: "", Expression: expr}
	if err := csRepo.Create(ctx, cs); err != nil {
		t.Fatalf("makeCS %q: %v", name, err)
	}
	return cs
}

// makePriv builds a Privilege value (does not insert).
func makePriv(name string, csID *string) *domain.Privilege {
	return &domain.Privilege{
		Name:              name,
		Description:       "desc-" + name,
		Type:              domain.PrivilegeTypeRepositoryContentSelector,
		Attrs:             map[string]any{"actions": []string{"read"}},
		ContentSelectorID: csID,
	}
}

// ── Create ────────────────────────────────────────────────────────────────────

func TestPrivilegeRepo_Create_PopulatesIDAndTimestamp(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	repo := NewPrivilegeRepo(pool)

	cs := makeCS(t, ctx, "create_ts_cs", `true`)
	p := makePriv("create_ts_priv", &cs.ID)

	if err := repo.Create(ctx, p); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if p.ID == "" {
		t.Fatal("Create did not populate ID")
	}
	if p.CreatedAt.IsZero() {
		t.Fatal("Create did not populate CreatedAt")
	}
}

func TestPrivilegeRepo_Create_FieldsRoundTrip(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	repo := NewPrivilegeRepo(pool)

	cs := makeCS(t, ctx, "roundtrip_cs", `format == "maven2"`)
	p := &domain.Privilege{
		Name:              "roundtrip_priv",
		Description:       "A round-trip privilege",
		Type:              domain.PrivilegeTypeRepositoryContentSelector,
		Attrs:             map[string]any{"actions": []string{"read", "write"}},
		ContentSelectorID: &cs.ID,
	}
	if err := repo.Create(ctx, p); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.Get(ctx, p.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.Name != p.Name {
		t.Errorf("Name: got %q, want %q", got.Name, p.Name)
	}
	if got.Description != p.Description {
		t.Errorf("Description: got %q, want %q", got.Description, p.Description)
	}
	if got.Type != p.Type {
		t.Errorf("Type: got %q, want %q", got.Type, p.Type)
	}
	if got.ContentSelectorID == nil || *got.ContentSelectorID != cs.ID {
		t.Errorf("ContentSelectorID: got %v, want %s", got.ContentSelectorID, cs.ID)
	}
	if got.Builtin {
		t.Error("Builtin: expected false for non-builtin privilege")
	}
}

func TestPrivilegeRepo_Create_DuplicateName_Errors(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	repo := NewPrivilegeRepo(pool)

	cs := makeCS(t, ctx, "dup_priv_cs", `true`)
	p1 := makePriv("dup_priv", &cs.ID)
	if err := repo.Create(ctx, p1); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	p2 := makePriv("dup_priv", &cs.ID)
	if err := repo.Create(ctx, p2); err == nil {
		t.Fatal("expected error for duplicate privilege name, got nil")
	}
}

func TestPrivilegeRepo_Create_ContentSelectorIDRequired_ForContentSelectorType(t *testing.T) {
	// Migration 007 enforces that repository-content-selector type must have a non-NULL selector.
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	repo := NewPrivilegeRepo(pool)

	p := &domain.Privilege{
		Name:              "no_selector_priv",
		Type:              domain.PrivilegeTypeRepositoryContentSelector,
		Attrs:             map[string]any{},
		ContentSelectorID: nil, // violates CHECK constraint
	}
	if err := repo.Create(ctx, p); err == nil {
		t.Fatal("expected error: repository-content-selector without content_selector_id should fail")
	}
}

func TestPrivilegeRepo_Create_AttrsPreserved(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	repo := NewPrivilegeRepo(pool)

	cs := makeCS(t, ctx, "attrs_cs", `true`)
	p := &domain.Privilege{
		Name:              "attrs_priv",
		Type:              domain.PrivilegeTypeRepositoryContentSelector,
		Attrs:             map[string]any{"actions": []string{"read"}, "format": "maven2"},
		ContentSelectorID: &cs.ID,
	}
	if err := repo.Create(ctx, p); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.Get(ctx, p.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.Attrs == nil {
		t.Fatal("Attrs is nil")
	}
	if _, ok := got.Attrs["actions"]; !ok {
		t.Error("Attrs missing 'actions' key")
	}
}

// ── Get ───────────────────────────────────────────────────────────────────────

func TestPrivilegeRepo_Get_NotFound_ReturnsNil(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	repo := NewPrivilegeRepo(pool)

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

func TestPrivilegeRepo_GetByName_HappyPath(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	repo := NewPrivilegeRepo(pool)

	cs := makeCS(t, ctx, "getbyname_cs", `true`)
	p := makePriv("getbyname_priv", &cs.ID)
	if err := repo.Create(ctx, p); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.GetByName(ctx, "getbyname_priv")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if got == nil {
		t.Fatal("GetByName returned nil")
	}
	if got.ID != p.ID {
		t.Errorf("ID mismatch: got %q, want %q", got.ID, p.ID)
	}
	if got.Name != "getbyname_priv" {
		t.Errorf("Name: got %q, want getbyname_priv", got.Name)
	}
}

func TestPrivilegeRepo_GetByName_NotFound_ReturnsNil(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	repo := NewPrivilegeRepo(pool)

	got, err := repo.GetByName(ctx, "nonexistent_priv_xyz")
	if !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("GetByName(missing): want ErrNotFound, got %v", err)
	}
	if got != nil {
		t.Fatalf("GetByName(missing): expected nil, got %+v", got)
	}
}

// ── Update ────────────────────────────────────────────────────────────────────

func TestPrivilegeRepo_Update_PersistsChanges(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	repo := NewPrivilegeRepo(pool)

	cs := makeCS(t, ctx, "update_priv_cs", `true`)
	p := makePriv("update_priv", &cs.ID)
	if err := repo.Create(ctx, p); err != nil {
		t.Fatalf("Create: %v", err)
	}

	p.Name = "updated_priv"
	p.Description = "updated description"
	p.Attrs = map[string]any{"actions": []string{"read", "delete"}}
	if err := repo.Update(ctx, p); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := repo.Get(ctx, p.ID)
	if err != nil {
		t.Fatalf("Get after Update: %v", err)
	}
	if got == nil {
		t.Fatal("Get after Update returned nil")
	}
	if got.Name != "updated_priv" {
		t.Errorf("Name: got %q, want updated_priv", got.Name)
	}
	if got.Description != "updated description" {
		t.Errorf("Description: got %q, want updated description", got.Description)
	}
}

// ── Delete ────────────────────────────────────────────────────────────────────

func TestPrivilegeRepo_Delete_RemovesPrivilege(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	repo := NewPrivilegeRepo(pool)

	cs := makeCS(t, ctx, "delete_priv_cs", `true`)
	p := makePriv("delete_priv", &cs.ID)
	if err := repo.Create(ctx, p); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.Delete(ctx, p.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	got, err := repo.Get(ctx, p.ID)
	if !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("Get after Delete: want ErrNotFound, got %v", err)
	}
	if got != nil {
		t.Fatal("Get after Delete: expected nil, privilege still exists")
	}
}

func TestPrivilegeRepo_Delete_BuiltinPrivilege_NoOp(t *testing.T) {
	// Delete uses AND builtin=false, so deleting a builtin privilege is a no-op (no error, no delete).
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()

	// Insert a builtin privilege directly via pool (bypassing the repo's Create which doesn't set builtin).
	cs := makeCS(t, ctx, "builtin_del_cs", `true`)
	csID := cs.ID
	var privID string
	err := pool.QueryRow(ctx,
		`INSERT INTO privileges (name, description, type, attrs, content_selector_id, builtin)
		 VALUES ($1, $2, $3, $4, $5, true) RETURNING id`,
		"builtin_del_priv", "", "repository-content-selector", `{}`, csID,
	).Scan(&privID)
	if err != nil {
		t.Fatalf("Insert builtin privilege: %v", err)
	}

	repo := NewPrivilegeRepo(pool)
	if err := repo.Delete(ctx, privID); err != nil {
		t.Fatalf("Delete(builtin): unexpected error: %v", err)
	}

	// Privilege should still be there because builtin=true is excluded.
	got, err := repo.Get(ctx, privID)
	if err != nil {
		t.Fatalf("Get after Delete(builtin): %v", err)
	}
	if got == nil {
		t.Fatal("Builtin privilege was unexpectedly deleted")
	}
}

func TestPrivilegeRepo_Delete_UnknownPrivilege_NoError(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	repo := NewPrivilegeRepo(pool)

	const missing = "00000000-0000-0000-0000-000000000000"
	if err := repo.Delete(ctx, missing); err != nil {
		t.Fatalf("Delete(missing): unexpected error: %v", err)
	}
}

// ── List ──────────────────────────────────────────────────────────────────────

func TestPrivilegeRepo_List_Empty(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	repo := NewPrivilegeRepo(pool)

	privs, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(privs) != 0 {
		t.Fatalf("expected empty list, got %d", len(privs))
	}
}

func TestPrivilegeRepo_List_OrderedByName(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	repo := NewPrivilegeRepo(pool)

	cs := makeCS(t, ctx, "list_order_cs", `true`)
	for _, name := range []string{"zzz_priv", "aaa_priv", "mmm_priv"} {
		p := makePriv(name, &cs.ID)
		if err := repo.Create(ctx, p); err != nil {
			t.Fatalf("Create %q: %v", name, err)
		}
	}

	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("List: got %d, want 3", len(list))
	}
	if list[0].Name != "aaa_priv" || list[1].Name != "mmm_priv" || list[2].Name != "zzz_priv" {
		t.Errorf("List not ordered: got %q %q %q", list[0].Name, list[1].Name, list[2].Name)
	}
}

func TestPrivilegeRepo_List_ContainsCreatedPrivilege(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	repo := NewPrivilegeRepo(pool)

	cs := makeCS(t, ctx, "list_contains_cs", `true`)
	p := makePriv("list_contains_priv", &cs.ID)
	if err := repo.Create(ctx, p); err != nil {
		t.Fatalf("Create: %v", err)
	}

	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List: got %d, want 1", len(list))
	}
	if list[0].ID != p.ID {
		t.Errorf("ID mismatch: got %q, want %q", list[0].ID, p.ID)
	}
	if list[0].ContentSelectorID == nil || *list[0].ContentSelectorID != cs.ID {
		t.Errorf("ContentSelectorID: got %v, want %s", list[0].ContentSelectorID, cs.ID)
	}
}

// ── ListByRole ────────────────────────────────────────────────────────────────

func TestPrivilegeRepo_ListByRole_Empty(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	privRepo := NewPrivilegeRepo(pool)
	roleRepo := NewRoleRepo(pool)

	role := makeRole("listbyrole_empty_role", "local")
	if err := roleRepo.Create(ctx, role); err != nil {
		t.Fatalf("Create role: %v", err)
	}

	privs, err := privRepo.ListByRole(ctx, role.ID)
	if err != nil {
		t.Fatalf("ListByRole: %v", err)
	}
	if len(privs) != 0 {
		t.Errorf("expected empty, got %v", privs)
	}
}

func TestPrivilegeRepo_ListByRole_AfterSetPrivileges(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	privRepo := NewPrivilegeRepo(pool)
	roleRepo := NewRoleRepo(pool)

	cs := makeCS(t, ctx, "listbyrole_cs", `true`)

	p1 := makePriv("listbyrole_p1", &cs.ID)
	p2 := makePriv("listbyrole_p2", &cs.ID)
	if err := privRepo.Create(ctx, p1); err != nil {
		t.Fatalf("Create p1: %v", err)
	}
	if err := privRepo.Create(ctx, p2); err != nil {
		t.Fatalf("Create p2: %v", err)
	}

	role := makeRole("listbyrole_role", "local")
	if err := roleRepo.Create(ctx, role); err != nil {
		t.Fatalf("Create role: %v", err)
	}
	if err := roleRepo.SetPrivileges(ctx, role.ID, []string{p1.ID, p2.ID}); err != nil {
		t.Fatalf("SetPrivileges: %v", err)
	}

	privs, err := privRepo.ListByRole(ctx, role.ID)
	if err != nil {
		t.Fatalf("ListByRole: %v", err)
	}
	if len(privs) != 2 {
		t.Fatalf("ListByRole: got %d, want 2", len(privs))
	}
	// ListByRole orders by name.
	if privs[0].Name != "listbyrole_p1" || privs[1].Name != "listbyrole_p2" {
		t.Errorf("ListByRole order: got %q %q", privs[0].Name, privs[1].Name)
	}
}

func TestPrivilegeRepo_ListByRole_UnknownRole_ReturnsEmpty(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	privRepo := NewPrivilegeRepo(pool)

	const missing = "00000000-0000-0000-0000-000000000000"
	privs, err := privRepo.ListByRole(ctx, missing)
	if err != nil {
		t.Fatalf("ListByRole(missing): unexpected error: %v", err)
	}
	if len(privs) != 0 {
		t.Errorf("ListByRole(missing): expected empty, got %v", privs)
	}
}

// ── PrivilegeRoleMap ──────────────────────────────────────────────────────────

func TestPrivilegeRepo_PrivilegeRoleMap_Empty(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	repo := NewPrivilegeRepo(pool)

	m, err := repo.PrivilegeRoleMap(ctx)
	if err != nil {
		t.Fatalf("PrivilegeRoleMap: %v", err)
	}
	if len(m) != 0 {
		t.Errorf("expected empty map, got %v", m)
	}
}

func TestPrivilegeRepo_PrivilegeRoleMap_PopulatedAfterAssignment(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	privRepo := NewPrivilegeRepo(pool)
	roleRepo := NewRoleRepo(pool)

	cs := makeCS(t, ctx, "rolemap_cs", `true`)
	p := makePriv("rolemap_priv", &cs.ID)
	if err := privRepo.Create(ctx, p); err != nil {
		t.Fatalf("Create priv: %v", err)
	}

	r1 := makeRole("rolemap_r1", "local")
	r2 := makeRole("rolemap_r2", "local")
	if err := roleRepo.Create(ctx, r1); err != nil {
		t.Fatalf("Create r1: %v", err)
	}
	if err := roleRepo.Create(ctx, r2); err != nil {
		t.Fatalf("Create r2: %v", err)
	}

	// Assign the same privilege to both roles.
	if err := roleRepo.SetPrivileges(ctx, r1.ID, []string{p.ID}); err != nil {
		t.Fatalf("SetPrivileges r1: %v", err)
	}
	if err := roleRepo.SetPrivileges(ctx, r2.ID, []string{p.ID}); err != nil {
		t.Fatalf("SetPrivileges r2: %v", err)
	}

	m, err := privRepo.PrivilegeRoleMap(ctx)
	if err != nil {
		t.Fatalf("PrivilegeRoleMap: %v", err)
	}
	if len(m) != 1 {
		t.Fatalf("PrivilegeRoleMap: got %d entries, want 1", len(m))
	}
	roleNames, ok := m[p.ID]
	if !ok {
		t.Fatalf("PrivilegeRoleMap: priv %q not in map", p.ID)
	}
	if len(roleNames) != 2 {
		t.Fatalf("PrivilegeRoleMap[priv]: got %d roles, want 2: %v", len(roleNames), roleNames)
	}
}

func TestPrivilegeRepo_PrivilegeRoleMap_PrivNotInAnyRole_NotInMap(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	repo := NewPrivilegeRepo(pool)

	cs := makeCS(t, ctx, "notinrole_cs", `true`)
	p := makePriv("notinrole_priv", &cs.ID)
	if err := repo.Create(ctx, p); err != nil {
		t.Fatalf("Create priv: %v", err)
	}

	m, err := repo.PrivilegeRoleMap(ctx)
	if err != nil {
		t.Fatalf("PrivilegeRoleMap: %v", err)
	}
	if _, ok := m[p.ID]; ok {
		t.Errorf("expected priv %q not in map, but found it: %v", p.ID, m[p.ID])
	}
}
