//go:build integration

package postgres

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/testutil/pgtest"
)

// ── helpers ──────────────────────────────────────────────────────────────────

func makeRole(name, source string) *domain.Role {
	return &domain.Role{
		Name:        name,
		Description: "desc-" + name,
		Source:      source,
		ReadOnly:    false,
	}
}

// ── Create ────────────────────────────────────────────────────────────────────

func TestRoleRepo_Create_PopulatesIDAndTimestamps(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	repo := NewRoleRepo(pool)

	r := makeRole("create_role_1", "local")
	if err := repo.Create(ctx, r); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if r.ID == "" {
		t.Fatal("Create did not populate ID")
	}
	if r.CreatedAt.IsZero() {
		t.Fatal("Create did not populate CreatedAt")
	}
	if r.UpdatedAt.IsZero() {
		t.Fatal("Create did not populate UpdatedAt")
	}
}

func TestRoleRepo_Create_FieldsRoundTrip(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	repo := NewRoleRepo(pool)

	r := &domain.Role{
		Name:        "roundtrip_role",
		Description: "A roundtrip role",
		Source:      "ldap",
		ReadOnly:    false,
	}
	if err := repo.Create(ctx, r); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.Get(ctx, r.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.Name != r.Name {
		t.Errorf("Name: got %q, want %q", got.Name, r.Name)
	}
	if got.Description != r.Description {
		t.Errorf("Description: got %q, want %q", got.Description, r.Description)
	}
	if got.Source != r.Source {
		t.Errorf("Source: got %q, want %q", got.Source, r.Source)
	}
	if got.ReadOnly != r.ReadOnly {
		t.Errorf("ReadOnly: got %v, want %v", got.ReadOnly, r.ReadOnly)
	}
	if got.ID != r.ID {
		t.Errorf("ID: got %q, want %q", got.ID, r.ID)
	}
}

func TestRoleRepo_Create_BuiltinFlag(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	repo := NewRoleRepo(pool)

	r := &domain.Role{
		Name:        "builtin_test_role",
		Description: "A builtin role",
		Source:      "local",
		ReadOnly:    true,
	}
	if err := repo.Create(ctx, r); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.Get(ctx, r.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if !got.ReadOnly {
		t.Errorf("ReadOnly: got false, want true")
	}
}

func TestRoleRepo_Create_DuplicateName_Errors(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	repo := NewRoleRepo(pool)

	r1 := makeRole("dup_role", "local")
	if err := repo.Create(ctx, r1); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	r2 := makeRole("dup_role", "local")
	if err := repo.Create(ctx, r2); err == nil {
		t.Fatal("expected error for duplicate role name, got nil")
	}
}

// ── Get ───────────────────────────────────────────────────────────────────────

func TestRoleRepo_Get_NotFound_ReturnsNil(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	repo := NewRoleRepo(pool)

	const missing = "00000000-0000-0000-0000-000000000000"
	got, err := repo.Get(ctx, missing)
	if !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("Get(missing): want ErrNotFound, got %v", err)
	}
	if got != nil {
		t.Fatalf("Get(missing): expected nil, got %+v", got)
	}
}

// ── Update ────────────────────────────────────────────────────────────────────

func TestRoleRepo_Update_PersistsChanges(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	repo := NewRoleRepo(pool)

	r := makeRole("update_role", "local")
	if err := repo.Create(ctx, r); err != nil {
		t.Fatalf("Create: %v", err)
	}

	r.Name = "updated_role"
	r.Description = "updated description"
	if err := repo.Update(ctx, r); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := repo.Get(ctx, r.ID)
	if err != nil {
		t.Fatalf("Get after Update: %v", err)
	}
	if got == nil {
		t.Fatal("Get after Update returned nil")
	}
	if got.Name != "updated_role" {
		t.Errorf("Name after update: got %q, want %q", got.Name, "updated_role")
	}
	if got.Description != "updated description" {
		t.Errorf("Description after update: got %q, want %q", got.Description, "updated description")
	}
}

// ── Delete ────────────────────────────────────────────────────────────────────

func TestRoleRepo_Delete_RemovesRole(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	repo := NewRoleRepo(pool)

	r := makeRole("delete_role", "local")
	if err := repo.Create(ctx, r); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.Delete(ctx, r.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	got, err := repo.Get(ctx, r.ID)
	if !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("Get after Delete: want ErrNotFound, got %v", err)
	}
	if got != nil {
		t.Fatal("Get after Delete: expected nil, role still exists")
	}
}

func TestRoleRepo_Delete_UnknownRole_NoError(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	repo := NewRoleRepo(pool)

	const missing = "00000000-0000-0000-0000-000000000000"
	if err := repo.Delete(ctx, missing); err != nil {
		t.Fatalf("Delete(missing): unexpected error: %v", err)
	}
}

// ── List ──────────────────────────────────────────────────────────────────────

func TestRoleRepo_List_Empty(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	repo := NewRoleRepo(pool)

	roles, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(roles) != 0 {
		t.Fatalf("expected empty list, got %d", len(roles))
	}
}

func TestRoleRepo_List_ReturnsAll(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	repo := NewRoleRepo(pool)

	names := []string{"list_role_c", "list_role_a", "list_role_b"}
	for _, name := range names {
		r := makeRole(name, "local")
		if err := repo.Create(ctx, r); err != nil {
			t.Fatalf("Create %q: %v", name, err)
		}
	}

	all, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("List: got %d, want 3", len(all))
	}
}

func TestRoleRepo_List_OrderedByName(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	repo := NewRoleRepo(pool)

	for _, name := range []string{"zzz_role", "aaa_role", "mmm_role"} {
		if err := repo.Create(ctx, makeRole(name, "local")); err != nil {
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
	if list[0].Name != "aaa_role" || list[1].Name != "mmm_role" || list[2].Name != "zzz_role" {
		t.Errorf("List not ordered: got %q %q %q", list[0].Name, list[1].Name, list[2].Name)
	}
}

func TestRoleRepo_List_IncludesPrivilegeIDs(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	roleRepo := NewRoleRepo(pool)
	privRepo := NewPrivilegeRepo(pool)
	csRepo := NewContentSelectorRepo(pool)

	// Create a content selector needed by the privilege.
	cs := &domain.ContentSelector{
		Name:        "list_privids_cs",
		Description: "test",
		Expression:  `format == "raw"`,
	}
	if err := csRepo.Create(ctx, cs); err != nil {
		t.Fatalf("Create content selector: %v", err)
	}

	priv := &domain.Privilege{
		Name:              "list_privids_priv",
		Description:       "priv for list test",
		Type:              domain.PrivilegeTypeRepositoryContentSelector,
		Attrs:             map[string]any{"actions": []string{"read"}},
		ContentSelectorID: &cs.ID,
	}
	if err := privRepo.Create(ctx, priv); err != nil {
		t.Fatalf("Create privilege: %v", err)
	}

	role := makeRole("list_with_privs_role", "local")
	if err := roleRepo.Create(ctx, role); err != nil {
		t.Fatalf("Create role: %v", err)
	}
	if err := roleRepo.SetPrivileges(ctx, role.ID, []string{priv.ID}); err != nil {
		t.Fatalf("SetPrivileges: %v", err)
	}

	list, err := roleRepo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List: got %d, want 1", len(list))
	}
	if len(list[0].Privileges) != 1 || list[0].Privileges[0] != priv.ID {
		t.Errorf("List Privileges: got %v, want [%s]", list[0].Privileges, priv.ID)
	}
}

// ── SetPrivileges / ListPrivilegeIDsByRole ────────────────────────────────────

func TestRoleRepo_SetPrivileges_AssignAndRead(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	roleRepo := NewRoleRepo(pool)
	privRepo := NewPrivilegeRepo(pool)
	csRepo := NewContentSelectorRepo(pool)

	cs := &domain.ContentSelector{
		Name: "setpriv_cs_1", Description: "", Expression: `path.startsWith("/")`,
	}
	if err := csRepo.Create(ctx, cs); err != nil {
		t.Fatalf("Create cs: %v", err)
	}

	p1 := &domain.Privilege{
		Name: "setpriv_p1", Type: domain.PrivilegeTypeRepositoryContentSelector,
		Attrs: map[string]any{}, ContentSelectorID: &cs.ID,
	}
	p2 := &domain.Privilege{
		Name: "setpriv_p2", Type: domain.PrivilegeTypeRepositoryContentSelector,
		Attrs: map[string]any{}, ContentSelectorID: &cs.ID,
	}
	if err := privRepo.Create(ctx, p1); err != nil {
		t.Fatalf("Create p1: %v", err)
	}
	if err := privRepo.Create(ctx, p2); err != nil {
		t.Fatalf("Create p2: %v", err)
	}

	role := makeRole("setpriv_role", "local")
	if err := roleRepo.Create(ctx, role); err != nil {
		t.Fatalf("Create role: %v", err)
	}

	if err := roleRepo.SetPrivileges(ctx, role.ID, []string{p1.ID, p2.ID}); err != nil {
		t.Fatalf("SetPrivileges: %v", err)
	}

	ids, err := roleRepo.ListPrivilegeIDsByRole(ctx, role.ID)
	if err != nil {
		t.Fatalf("ListPrivilegeIDsByRole: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("ListPrivilegeIDsByRole: got %d, want 2", len(ids))
	}
	sort.Strings(ids)
	wantIDs := []string{p1.ID, p2.ID}
	sort.Strings(wantIDs)
	if ids[0] != wantIDs[0] || ids[1] != wantIDs[1] {
		t.Errorf("ListPrivilegeIDsByRole: got %v, want %v", ids, wantIDs)
	}
}

func TestRoleRepo_SetPrivileges_Replace(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	roleRepo := NewRoleRepo(pool)
	privRepo := NewPrivilegeRepo(pool)
	csRepo := NewContentSelectorRepo(pool)

	cs := &domain.ContentSelector{
		Name: "replace_priv_cs", Expression: `true`,
	}
	if err := csRepo.Create(ctx, cs); err != nil {
		t.Fatalf("Create cs: %v", err)
	}

	p1 := &domain.Privilege{
		Name: "replace_priv_p1", Type: domain.PrivilegeTypeRepositoryContentSelector,
		Attrs: map[string]any{}, ContentSelectorID: &cs.ID,
	}
	p2 := &domain.Privilege{
		Name: "replace_priv_p2", Type: domain.PrivilegeTypeRepositoryContentSelector,
		Attrs: map[string]any{}, ContentSelectorID: &cs.ID,
	}
	if err := privRepo.Create(ctx, p1); err != nil {
		t.Fatalf("Create p1: %v", err)
	}
	if err := privRepo.Create(ctx, p2); err != nil {
		t.Fatalf("Create p2: %v", err)
	}

	role := makeRole("replace_priv_role", "local")
	if err := roleRepo.Create(ctx, role); err != nil {
		t.Fatalf("Create role: %v", err)
	}

	// Assign p1 first.
	if err := roleRepo.SetPrivileges(ctx, role.ID, []string{p1.ID}); err != nil {
		t.Fatalf("SetPrivileges (first): %v", err)
	}

	// Replace with p2 only.
	if err := roleRepo.SetPrivileges(ctx, role.ID, []string{p2.ID}); err != nil {
		t.Fatalf("SetPrivileges (replace): %v", err)
	}

	ids, err := roleRepo.ListPrivilegeIDsByRole(ctx, role.ID)
	if err != nil {
		t.Fatalf("ListPrivilegeIDsByRole: %v", err)
	}
	if len(ids) != 1 || ids[0] != p2.ID {
		t.Errorf("After replace: got %v, want [%s]", ids, p2.ID)
	}
}

func TestRoleRepo_SetPrivileges_ClearAll(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	roleRepo := NewRoleRepo(pool)
	privRepo := NewPrivilegeRepo(pool)
	csRepo := NewContentSelectorRepo(pool)

	cs := &domain.ContentSelector{Name: "clear_priv_cs", Expression: `true`}
	if err := csRepo.Create(ctx, cs); err != nil {
		t.Fatalf("Create cs: %v", err)
	}

	p := &domain.Privilege{
		Name: "clear_priv_p", Type: domain.PrivilegeTypeRepositoryContentSelector,
		Attrs: map[string]any{}, ContentSelectorID: &cs.ID,
	}
	if err := privRepo.Create(ctx, p); err != nil {
		t.Fatalf("Create priv: %v", err)
	}

	role := makeRole("clear_priv_role", "local")
	if err := roleRepo.Create(ctx, role); err != nil {
		t.Fatalf("Create role: %v", err)
	}

	if err := roleRepo.SetPrivileges(ctx, role.ID, []string{p.ID}); err != nil {
		t.Fatalf("SetPrivileges: %v", err)
	}

	// Clear all privileges.
	if err := roleRepo.SetPrivileges(ctx, role.ID, []string{}); err != nil {
		t.Fatalf("SetPrivileges (clear): %v", err)
	}

	ids, err := roleRepo.ListPrivilegeIDsByRole(ctx, role.ID)
	if err != nil {
		t.Fatalf("ListPrivilegeIDsByRole: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("ListPrivilegeIDsByRole after clear: got %v, want empty", ids)
	}
}

func TestRoleRepo_ListPrivilegeIDsByRole_Empty(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	roleRepo := NewRoleRepo(pool)

	role := makeRole("empty_privs_role", "local")
	if err := roleRepo.Create(ctx, role); err != nil {
		t.Fatalf("Create role: %v", err)
	}

	ids, err := roleRepo.ListPrivilegeIDsByRole(ctx, role.ID)
	if err != nil {
		t.Fatalf("ListPrivilegeIDsByRole: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("expected empty slice, got %v", ids)
	}
}

// ── GetUserRoles / SetUserRoles ───────────────────────────────────────────────

func TestRoleRepo_GetUserRoles_Empty(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	roleRepo := NewRoleRepo(pool)

	// Use a non-existent UUID — should return empty slice, not error.
	const fakeUserID = "00000000-0000-0000-0000-000000000099"
	roles, err := roleRepo.GetUserRoles(ctx, fakeUserID)
	if err != nil {
		t.Fatalf("GetUserRoles(empty): %v", err)
	}
	if len(roles) != 0 {
		t.Errorf("expected empty, got %v", roles)
	}
}

func TestRoleRepo_GetUserRoles_AfterSetUserRoles(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	userRepo := NewUserRepo(pool)
	roleRepo := NewRoleRepo(pool)

	r1 := makeRole("gu_role_1", "local")
	r2 := makeRole("gu_role_2", "local")
	if err := roleRepo.Create(ctx, r1); err != nil {
		t.Fatalf("Create r1: %v", err)
	}
	if err := roleRepo.Create(ctx, r2); err != nil {
		t.Fatalf("Create r2: %v", err)
	}

	u := makeUser("get_user_roles_user", "gu_roles@test.com")
	if err := userRepo.Create(ctx, u); err != nil {
		t.Fatalf("Create user: %v", err)
	}

	if err := roleRepo.SetUserRoles(ctx, u.ID, []string{r1.ID, r2.ID}); err != nil {
		t.Fatalf("SetUserRoles: %v", err)
	}

	roles, err := roleRepo.GetUserRoles(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetUserRoles: %v", err)
	}
	if len(roles) != 2 {
		t.Fatalf("GetUserRoles: got %d, want 2", len(roles))
	}
	names := []string{roles[0].Name, roles[1].Name}
	sort.Strings(names)
	if names[0] != "gu_role_1" || names[1] != "gu_role_2" {
		t.Errorf("GetUserRoles names: got %v", names)
	}
}

func TestRoleRepo_GetUserRoles_FullRoleFields(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	userRepo := NewUserRepo(pool)
	roleRepo := NewRoleRepo(pool)

	r := &domain.Role{Name: "full_field_role", Description: "full desc", Source: "local", ReadOnly: false}
	if err := roleRepo.Create(ctx, r); err != nil {
		t.Fatalf("Create role: %v", err)
	}

	u := makeUser("full_fields_user", "full_fields@test.com")
	if err := userRepo.Create(ctx, u); err != nil {
		t.Fatalf("Create user: %v", err)
	}

	if err := roleRepo.SetUserRoles(ctx, u.ID, []string{r.ID}); err != nil {
		t.Fatalf("SetUserRoles: %v", err)
	}

	roles, err := roleRepo.GetUserRoles(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetUserRoles: %v", err)
	}
	if len(roles) != 1 {
		t.Fatalf("GetUserRoles: got %d, want 1", len(roles))
	}
	got := roles[0]
	if got.ID != r.ID {
		t.Errorf("ID: got %q, want %q", got.ID, r.ID)
	}
	if got.Name != "full_field_role" {
		t.Errorf("Name: got %q, want full_field_role", got.Name)
	}
	if got.Description != "full desc" {
		t.Errorf("Description: got %q, want full desc", got.Description)
	}
}

// ── Delete cascades role_privileges ──────────────────────────────────────────

func TestRoleRepo_Delete_CascadesRolePrivileges(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	roleRepo := NewRoleRepo(pool)
	privRepo := NewPrivilegeRepo(pool)
	csRepo := NewContentSelectorRepo(pool)

	cs := &domain.ContentSelector{Name: "cascade_del_cs", Expression: `true`}
	if err := csRepo.Create(ctx, cs); err != nil {
		t.Fatalf("Create cs: %v", err)
	}

	p := &domain.Privilege{
		Name: "cascade_del_priv", Type: domain.PrivilegeTypeRepositoryContentSelector,
		Attrs: map[string]any{}, ContentSelectorID: &cs.ID,
	}
	if err := privRepo.Create(ctx, p); err != nil {
		t.Fatalf("Create priv: %v", err)
	}

	role := makeRole("cascade_del_role", "local")
	if err := roleRepo.Create(ctx, role); err != nil {
		t.Fatalf("Create role: %v", err)
	}

	if err := roleRepo.SetPrivileges(ctx, role.ID, []string{p.ID}); err != nil {
		t.Fatalf("SetPrivileges: %v", err)
	}

	if err := roleRepo.Delete(ctx, role.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Privilege itself should still exist.
	gotPriv, err := privRepo.Get(ctx, p.ID)
	if err != nil {
		t.Fatalf("Get privilege after role delete: %v", err)
	}
	if gotPriv == nil {
		t.Fatal("Privilege unexpectedly deleted when role was deleted")
	}

	// role_privileges row should be gone (cascade).
	ids, err := roleRepo.ListPrivilegeIDsByRole(ctx, role.ID)
	if err != nil {
		t.Fatalf("ListPrivilegeIDsByRole after Delete: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("role_privileges not cascade-deleted: %v", ids)
	}
}
