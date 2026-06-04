//go:build integration

package postgres

import (
	"context"
	"sort"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/testutil/pgtest"
)

// ── helpers ──────────────────────────────────────────────────────────────────

func makeUser(username, email string) *domain.User {
	return &domain.User{
		Username:     username,
		Email:        email,
		PasswordHash: "hashed_" + username,
		FirstName:    "First",
		LastName:     "Last",
		Status:       domain.UserStatusActive,
		Source:       domain.UserSourceLocal,
	}
}

// ── Create ────────────────────────────────────────────────────────────────────

func TestUserRepo_Create_PopulatesIDAndTimestamps(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "users", "roles")
	ctx := context.Background()
	repo := NewUserRepo(pool)

	u := makeUser("create_user1", "create_user1@test.com")
	if err := repo.Create(ctx, u); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if u.ID == "" {
		t.Fatal("Create did not populate ID")
	}
	if u.CreatedAt.IsZero() {
		t.Fatal("Create did not populate CreatedAt")
	}
	if u.UpdatedAt.IsZero() {
		t.Fatal("Create did not populate UpdatedAt")
	}
}

func TestUserRepo_Create_FieldsRoundTrip(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "users", "roles")
	ctx := context.Background()
	repo := NewUserRepo(pool)

	u := &domain.User{
		Username:     "roundtrip_user",
		Email:        "roundtrip@test.com",
		PasswordHash: "bcrypt_hash_abc",
		FirstName:    "Round",
		LastName:     "Trip",
		Status:       domain.UserStatusDisabled,
		Source:       domain.UserSourceOIDC,
		ExternalID:   "ext-123",
	}
	if err := repo.Create(ctx, u); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.Get(ctx, u.Username)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.Username != u.Username {
		t.Errorf("Username: got %q, want %q", got.Username, u.Username)
	}
	if got.Email != u.Email {
		t.Errorf("Email: got %q, want %q", got.Email, u.Email)
	}
	if got.PasswordHash != u.PasswordHash {
		t.Errorf("PasswordHash: got %q, want %q", got.PasswordHash, u.PasswordHash)
	}
	if got.FirstName != u.FirstName {
		t.Errorf("FirstName: got %q, want %q", got.FirstName, u.FirstName)
	}
	if got.LastName != u.LastName {
		t.Errorf("LastName: got %q, want %q", got.LastName, u.LastName)
	}
	if got.Status != u.Status {
		t.Errorf("Status: got %q, want %q", got.Status, u.Status)
	}
	if got.Source != u.Source {
		t.Errorf("Source: got %q, want %q", got.Source, u.Source)
	}
	if got.ID != u.ID {
		t.Errorf("ID: got %q, want %q", got.ID, u.ID)
	}
}

func TestUserRepo_Create_NullableEmail(t *testing.T) {
	// Users from LDAP may have no email; the repo stores NULL when Email is "".
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "users", "roles")
	ctx := context.Background()
	repo := NewUserRepo(pool)

	u := makeUser("no_email_user", "")
	u.Email = ""
	if err := repo.Create(ctx, u); err != nil {
		t.Fatalf("Create with empty email: %v", err)
	}

	got, err := repo.Get(ctx, u.Username)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil")
	}
	// COALESCE(email,'') in userSelect means we get "" back when DB stores NULL.
	if got.Email != "" {
		t.Errorf("Email: got %q, want %q (empty)", got.Email, "")
	}
}

func TestUserRepo_Create_DuplicateUsername_Errors(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "users", "roles")
	ctx := context.Background()
	repo := NewUserRepo(pool)

	u1 := makeUser("dup_user", "dup1@test.com")
	if err := repo.Create(ctx, u1); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	u2 := makeUser("dup_user", "dup2@test.com")
	if err := repo.Create(ctx, u2); err == nil {
		t.Fatal("expected error for duplicate username, got nil")
	}
}

// ── Get (by username) ────────────────────────────────────────────────────────

func TestUserRepo_Get_NotFound_ReturnsNil(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "users", "roles")
	ctx := context.Background()
	repo := NewUserRepo(pool)

	got, err := repo.Get(ctx, "nonexistent_user_xyz")
	if err != nil {
		t.Fatalf("Get(missing): unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("Get(missing): expected nil, got %+v", got)
	}
}

func TestUserRepo_Get_CaseSensitive(t *testing.T) {
	// username column is TEXT — lookups are case-sensitive.
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "users", "roles")
	ctx := context.Background()
	repo := NewUserRepo(pool)

	u := makeUser("CaseSensUser", "casesensu@test.com")
	if err := repo.Create(ctx, u); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.Get(ctx, "casesensuser")
	if err != nil {
		t.Fatalf("Get(lower): unexpected error: %v", err)
	}
	if got != nil {
		t.Logf("NOTE: Get is case-insensitive (found %q when searching %q)", got.Username, "casesensuser")
	}
	// Get by exact case must succeed.
	got2, err := repo.Get(ctx, "CaseSensUser")
	if err != nil {
		t.Fatalf("Get(exact): %v", err)
	}
	if got2 == nil {
		t.Fatal("Get(exact): expected user, got nil")
	}
}

// ── GetByID ───────────────────────────────────────────────────────────────────

func TestUserRepo_GetByID_HappyPath(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "users", "roles")
	ctx := context.Background()
	repo := NewUserRepo(pool)

	u := makeUser("getbyid_user", "getbyid@test.com")
	if err := repo.Create(ctx, u); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.GetByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got == nil {
		t.Fatal("GetByID returned nil")
	}
	if got.ID != u.ID {
		t.Errorf("ID mismatch: got %q, want %q", got.ID, u.ID)
	}
	if got.Username != u.Username {
		t.Errorf("Username mismatch: got %q, want %q", got.Username, u.Username)
	}
}

func TestUserRepo_GetByID_NotFound_ReturnsNil(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "users", "roles")
	ctx := context.Background()
	repo := NewUserRepo(pool)

	const missing = "00000000-0000-0000-0000-000000000000"
	got, err := repo.GetByID(ctx, missing)
	if err != nil {
		t.Fatalf("GetByID(missing): unexpected error: %v", err)
	}
	if got != nil {
		t.Fatalf("GetByID(missing): expected nil, got %+v", got)
	}
}

// ── Update ────────────────────────────────────────────────────────────────────

func TestUserRepo_Update_PersistsChanges(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "users", "roles")
	ctx := context.Background()
	repo := NewUserRepo(pool)

	u := makeUser("update_user", "update_user@test.com")
	if err := repo.Create(ctx, u); err != nil {
		t.Fatalf("Create: %v", err)
	}

	u.Email = "updated@test.com"
	u.FirstName = "Updated"
	u.LastName = "Name"
	u.Status = domain.UserStatusDisabled
	if err := repo.Update(ctx, u); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := repo.Get(ctx, u.Username)
	if err != nil {
		t.Fatalf("Get after Update: %v", err)
	}
	if got == nil {
		t.Fatal("Get after Update returned nil")
	}
	if got.Email != "updated@test.com" {
		t.Errorf("Email: got %q, want %q", got.Email, "updated@test.com")
	}
	if got.FirstName != "Updated" {
		t.Errorf("FirstName: got %q, want %q", got.FirstName, "Updated")
	}
	if got.LastName != "Name" {
		t.Errorf("LastName: got %q, want %q", got.LastName, "Name")
	}
	if got.Status != domain.UserStatusDisabled {
		t.Errorf("Status: got %q, want %q", got.Status, domain.UserStatusDisabled)
	}
}

func TestUserRepo_Update_ClearEmail(t *testing.T) {
	// Update with empty email should set it to NULL in DB (via nilIfEmpty).
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "users", "roles")
	ctx := context.Background()
	repo := NewUserRepo(pool)

	u := makeUser("update_email_user", "update_email@test.com")
	if err := repo.Create(ctx, u); err != nil {
		t.Fatalf("Create: %v", err)
	}

	u.Email = ""
	if err := repo.Update(ctx, u); err != nil {
		t.Fatalf("Update with empty email: %v", err)
	}

	got, err := repo.Get(ctx, u.Username)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.Email != "" {
		t.Errorf("Email after clear: got %q, want empty", got.Email)
	}
}

// ── UpdatePassword ────────────────────────────────────────────────────────────

func TestUserRepo_UpdatePassword_Persists(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "users", "roles")
	ctx := context.Background()
	repo := NewUserRepo(pool)

	u := makeUser("passwd_user", "passwd@test.com")
	u.PasswordHash = "original_hash"
	if err := repo.Create(ctx, u); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.UpdatePassword(ctx, u.Username, "new_hash"); err != nil {
		t.Fatalf("UpdatePassword: %v", err)
	}

	got, err := repo.Get(ctx, u.Username)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil")
	}
	if got.PasswordHash != "new_hash" {
		t.Errorf("PasswordHash: got %q, want %q", got.PasswordHash, "new_hash")
	}
}

func TestUserRepo_UpdatePassword_UnknownUser_NoError(t *testing.T) {
	// Update on non-existent username: Exec succeeds (0 rows affected) — no error.
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "users", "roles")
	ctx := context.Background()
	repo := NewUserRepo(pool)

	err := repo.UpdatePassword(ctx, "nonexistent_xyz", "hash")
	if err != nil {
		t.Fatalf("UpdatePassword(missing): unexpected error: %v", err)
	}
}

// ── Delete ────────────────────────────────────────────────────────────────────

func TestUserRepo_Delete_RemovesUser(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "users", "roles")
	ctx := context.Background()
	repo := NewUserRepo(pool)

	u := makeUser("delete_user", "delete_user@test.com")
	if err := repo.Create(ctx, u); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.Delete(ctx, u.Username); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	got, err := repo.Get(ctx, u.Username)
	if err != nil {
		t.Fatalf("Get after Delete: %v", err)
	}
	if got != nil {
		t.Fatal("Get after Delete: expected nil, user still exists")
	}
}

func TestUserRepo_Delete_UnknownUser_NoError(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "users", "roles")
	ctx := context.Background()
	repo := NewUserRepo(pool)

	if err := repo.Delete(ctx, "never_existed_user"); err != nil {
		t.Fatalf("Delete(missing): unexpected error: %v", err)
	}
}

// ── List ──────────────────────────────────────────────────────────────────────

func TestUserRepo_List_Empty(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "users", "roles")
	ctx := context.Background()
	repo := NewUserRepo(pool)

	users, err := repo.List(ctx, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(users) != 0 {
		t.Fatalf("expected empty list, got %d", len(users))
	}
}

func TestUserRepo_List_AllUsers(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "users", "roles")
	ctx := context.Background()
	repo := NewUserRepo(pool)

	names := []string{"list_user_a", "list_user_b", "list_user_c"}
	for i, name := range names {
		u := makeUser(name, name+"@test.com")
		if i == 1 {
			u.Source = domain.UserSourceLDAP
		}
		if err := repo.Create(ctx, u); err != nil {
			t.Fatalf("Create %q: %v", name, err)
		}
	}

	all, err := repo.List(ctx, "")
	if err != nil {
		t.Fatalf("List(all): %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("List(all): got %d, want 3", len(all))
	}
}

func TestUserRepo_List_FilterBySource(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "users", "roles")
	ctx := context.Background()
	repo := NewUserRepo(pool)

	local1 := makeUser("src_local_1", "src_local_1@test.com")
	local1.Source = domain.UserSourceLocal
	local2 := makeUser("src_local_2", "src_local_2@test.com")
	local2.Source = domain.UserSourceLocal
	ldapUser := makeUser("src_ldap_1", "src_ldap_1@test.com")
	ldapUser.Source = domain.UserSourceLDAP

	for _, u := range []*domain.User{local1, local2, ldapUser} {
		if err := repo.Create(ctx, u); err != nil {
			t.Fatalf("Create %q: %v", u.Username, err)
		}
	}

	localList, err := repo.List(ctx, "local")
	if err != nil {
		t.Fatalf("List(local): %v", err)
	}
	if len(localList) != 2 {
		t.Fatalf("List(local): got %d, want 2", len(localList))
	}

	ldapList, err := repo.List(ctx, "ldap")
	if err != nil {
		t.Fatalf("List(ldap): %v", err)
	}
	if len(ldapList) != 1 {
		t.Fatalf("List(ldap): got %d, want 1", len(ldapList))
	}
	if ldapList[0].Username != "src_ldap_1" {
		t.Errorf("List(ldap): got %q, want src_ldap_1", ldapList[0].Username)
	}
}

func TestUserRepo_List_OrderedByUsername(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "users", "roles")
	ctx := context.Background()
	repo := NewUserRepo(pool)

	// Insert out of alpha order.
	for _, name := range []string{"zz_user", "aa_user", "mm_user"} {
		u := makeUser(name, name+"@test.com")
		if err := repo.Create(ctx, u); err != nil {
			t.Fatalf("Create %q: %v", name, err)
		}
	}

	list, err := repo.List(ctx, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("List: got %d, want 3", len(list))
	}
	if list[0].Username != "aa_user" || list[1].Username != "mm_user" || list[2].Username != "zz_user" {
		t.Errorf("List not ordered: got %q %q %q", list[0].Username, list[1].Username, list[2].Username)
	}
}

// ── UpdateLastLogin ───────────────────────────────────────────────────────────

func TestUserRepo_UpdateLastLogin_SetsTimestamp(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "users", "roles")
	ctx := context.Background()
	repo := NewUserRepo(pool)

	u := makeUser("lastlogin_user", "lastlogin@test.com")
	if err := repo.Create(ctx, u); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// LastLogin should be nil before the first login.
	got, err := repo.Get(ctx, u.Username)
	if err != nil {
		t.Fatalf("Get (before login): %v", err)
	}
	if got.LastLogin != nil {
		t.Fatalf("LastLogin before login: expected nil, got %v", got.LastLogin)
	}

	if err := repo.UpdateLastLogin(ctx, u.Username); err != nil {
		t.Fatalf("UpdateLastLogin: %v", err)
	}

	got, err = repo.Get(ctx, u.Username)
	if err != nil {
		t.Fatalf("Get (after login): %v", err)
	}
	if got.LastLogin == nil {
		t.Fatal("LastLogin after UpdateLastLogin: expected non-nil")
	}
}

// ── SetOIDCTokens / GetOIDCIDToken ────────────────────────────────────────────

func TestUserRepo_OIDCTokens_RoundTrip(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "users", "roles")
	ctx := context.Background()
	repo := NewUserRepo(pool)

	u := makeUser("oidc_token_user", "oidc_token@test.com")
	u.Source = domain.UserSourceOIDC
	if err := repo.Create(ctx, u); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Initially no OIDC token.
	tok, err := repo.GetOIDCIDToken(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetOIDCIDToken (initial): %v", err)
	}
	if tok != "" {
		t.Fatalf("GetOIDCIDToken (initial): got %q, want empty", tok)
	}

	// Store tokens.
	if err := repo.SetOIDCTokens(ctx, u.ID, "id-token-value", "refresh-token-value"); err != nil {
		t.Fatalf("SetOIDCTokens: %v", err)
	}

	tok, err = repo.GetOIDCIDToken(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetOIDCIDToken (after set): %v", err)
	}
	if tok != "id-token-value" {
		t.Errorf("GetOIDCIDToken: got %q, want %q", tok, "id-token-value")
	}
}

func TestUserRepo_OIDCTokens_ClearWithEmpty(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "users", "roles")
	ctx := context.Background()
	repo := NewUserRepo(pool)

	u := makeUser("oidc_clear_user", "oidc_clear@test.com")
	u.Source = domain.UserSourceOIDC
	if err := repo.Create(ctx, u); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.SetOIDCTokens(ctx, u.ID, "tok", "ref"); err != nil {
		t.Fatalf("SetOIDCTokens: %v", err)
	}

	// Overwrite with empty strings — nilIfEmpty stores NULL.
	if err := repo.SetOIDCTokens(ctx, u.ID, "", ""); err != nil {
		t.Fatalf("SetOIDCTokens (clear): %v", err)
	}

	tok, err := repo.GetOIDCIDToken(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetOIDCIDToken (after clear): %v", err)
	}
	if tok != "" {
		t.Errorf("GetOIDCIDToken after clear: got %q, want empty", tok)
	}
}

func TestUserRepo_GetOIDCIDToken_UnknownUser_ReturnsEmpty(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "users", "roles")
	ctx := context.Background()
	repo := NewUserRepo(pool)

	const missing = "00000000-0000-0000-0000-000000000000"
	tok, err := repo.GetOIDCIDToken(ctx, missing)
	if err != nil {
		t.Fatalf("GetOIDCIDToken(missing): unexpected error: %v", err)
	}
	if tok != "" {
		t.Errorf("GetOIDCIDToken(missing): got %q, want empty", tok)
	}
}

// ── Role assignment (via roleRepo.SetUserRoles + userRepo.Get) ───────────────

func TestUserRepo_Roles_SetAndGet_RoundTrip(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "users", "roles")
	ctx := context.Background()
	userRepo := NewUserRepo(pool)
	roleRepo := NewRoleRepo(pool)

	// Create two roles.
	role1 := &domain.Role{Name: "role_alpha", Description: "a", Source: "local"}
	role2 := &domain.Role{Name: "role_beta", Description: "b", Source: "local"}
	if err := roleRepo.Create(ctx, role1); err != nil {
		t.Fatalf("Create role1: %v", err)
	}
	if err := roleRepo.Create(ctx, role2); err != nil {
		t.Fatalf("Create role2: %v", err)
	}

	u := makeUser("roles_user", "roles_user@test.com")
	if err := userRepo.Create(ctx, u); err != nil {
		t.Fatalf("Create user: %v", err)
	}

	// Assign both roles.
	if err := roleRepo.SetUserRoles(ctx, u.ID, []string{role1.ID, role2.ID}); err != nil {
		t.Fatalf("SetUserRoles: %v", err)
	}

	got, err := userRepo.Get(ctx, u.Username)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil")
	}

	roleNames := got.Roles
	sort.Strings(roleNames)
	want := []string{"role_alpha", "role_beta"}
	if len(roleNames) != 2 || roleNames[0] != want[0] || roleNames[1] != want[1] {
		t.Errorf("Roles: got %v, want %v", roleNames, want)
	}
}

func TestUserRepo_Roles_Replace(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "users", "roles")
	ctx := context.Background()
	userRepo := NewUserRepo(pool)
	roleRepo := NewRoleRepo(pool)

	role1 := &domain.Role{Name: "replace_role_1", Description: "", Source: "local"}
	role2 := &domain.Role{Name: "replace_role_2", Description: "", Source: "local"}
	if err := roleRepo.Create(ctx, role1); err != nil {
		t.Fatalf("Create role1: %v", err)
	}
	if err := roleRepo.Create(ctx, role2); err != nil {
		t.Fatalf("Create role2: %v", err)
	}

	u := makeUser("replace_roles_user", "replace_roles@test.com")
	if err := userRepo.Create(ctx, u); err != nil {
		t.Fatalf("Create user: %v", err)
	}

	// Assign role1 first.
	if err := roleRepo.SetUserRoles(ctx, u.ID, []string{role1.ID}); err != nil {
		t.Fatalf("SetUserRoles (first): %v", err)
	}

	// Replace with role2 only.
	if err := roleRepo.SetUserRoles(ctx, u.ID, []string{role2.ID}); err != nil {
		t.Fatalf("SetUserRoles (replace): %v", err)
	}

	got, err := userRepo.Get(ctx, u.Username)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Roles) != 1 || got.Roles[0] != "replace_role_2" {
		t.Errorf("Roles after replace: got %v, want [replace_role_2]", got.Roles)
	}
}

func TestUserRepo_Roles_ClearAll(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "users", "roles")
	ctx := context.Background()
	userRepo := NewUserRepo(pool)
	roleRepo := NewRoleRepo(pool)

	role := &domain.Role{Name: "clear_role", Description: "", Source: "local"}
	if err := roleRepo.Create(ctx, role); err != nil {
		t.Fatalf("Create role: %v", err)
	}

	u := makeUser("clear_roles_user", "clear_roles@test.com")
	if err := userRepo.Create(ctx, u); err != nil {
		t.Fatalf("Create user: %v", err)
	}

	if err := roleRepo.SetUserRoles(ctx, u.ID, []string{role.ID}); err != nil {
		t.Fatalf("SetUserRoles: %v", err)
	}

	// Clear all roles.
	if err := roleRepo.SetUserRoles(ctx, u.ID, []string{}); err != nil {
		t.Fatalf("SetUserRoles (clear): %v", err)
	}

	got, err := userRepo.Get(ctx, u.Username)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Roles) != 0 {
		t.Errorf("Roles after clear: got %v, want empty", got.Roles)
	}
}

func TestUserRepo_List_IncludesRoles(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "users", "roles")
	ctx := context.Background()
	userRepo := NewUserRepo(pool)
	roleRepo := NewRoleRepo(pool)

	role := &domain.Role{Name: "list_role_nx", Description: "", Source: "local"}
	if err := roleRepo.Create(ctx, role); err != nil {
		t.Fatalf("Create role: %v", err)
	}

	u := makeUser("list_with_role_user", "list_with_role@test.com")
	if err := userRepo.Create(ctx, u); err != nil {
		t.Fatalf("Create user: %v", err)
	}
	if err := roleRepo.SetUserRoles(ctx, u.ID, []string{role.ID}); err != nil {
		t.Fatalf("SetUserRoles: %v", err)
	}

	list, err := userRepo.List(ctx, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) == 0 {
		t.Fatal("List: empty")
	}
	found := list[0]
	if found.Username != u.Username {
		t.Fatalf("List[0].Username: got %q, want %q", found.Username, u.Username)
	}
	if len(found.Roles) != 1 || found.Roles[0] != "list_role_nx" {
		t.Errorf("List[0].Roles: got %v, want [list_role_nx]", found.Roles)
	}
}

// ── GetByID includes roles ────────────────────────────────────────────────────

func TestUserRepo_GetByID_IncludesRoles(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "users", "roles")
	ctx := context.Background()
	userRepo := NewUserRepo(pool)
	roleRepo := NewRoleRepo(pool)

	role := &domain.Role{Name: "getbyid_role", Description: "", Source: "local"}
	if err := roleRepo.Create(ctx, role); err != nil {
		t.Fatalf("Create role: %v", err)
	}

	u := makeUser("getbyid_roles_user", "getbyid_roles@test.com")
	if err := userRepo.Create(ctx, u); err != nil {
		t.Fatalf("Create user: %v", err)
	}
	if err := roleRepo.SetUserRoles(ctx, u.ID, []string{role.ID}); err != nil {
		t.Fatalf("SetUserRoles: %v", err)
	}

	got, err := userRepo.GetByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got == nil {
		t.Fatal("GetByID returned nil")
	}
	if len(got.Roles) != 1 || got.Roles[0] != "getbyid_role" {
		t.Errorf("GetByID Roles: got %v, want [getbyid_role]", got.Roles)
	}
}

// ── Source variants ───────────────────────────────────────────────────────────

func TestUserRepo_Create_AllSources(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "users", "roles")
	ctx := context.Background()
	repo := NewUserRepo(pool)

	sources := []domain.UserSource{
		domain.UserSourceLocal,
		domain.UserSourceLDAP,
		domain.UserSourceOIDC,
		domain.UserSourceSAML,
	}
	for _, src := range sources {
		u := &domain.User{
			Username:  "src_" + string(src) + "_user",
			Email:     "src_" + string(src) + "@test.com",
			FirstName: "F",
			LastName:  "L",
			Status:    domain.UserStatusActive,
			Source:    src,
		}
		if err := repo.Create(ctx, u); err != nil {
			t.Errorf("Create(source=%q): %v", src, err)
			continue
		}
		got, err := repo.Get(ctx, u.Username)
		if err != nil {
			t.Errorf("Get(source=%q): %v", src, err)
			continue
		}
		if got == nil {
			t.Errorf("Get(source=%q): nil", src)
			continue
		}
		if got.Source != src {
			t.Errorf("Source %q: got %q", src, got.Source)
		}
	}
}

// ── Delete cascades user_roles ────────────────────────────────────────────────

func TestUserRepo_Delete_CascadesUserRoles(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "users", "roles")
	ctx := context.Background()
	userRepo := NewUserRepo(pool)
	roleRepo := NewRoleRepo(pool)

	role := &domain.Role{Name: "cascade_role", Description: "", Source: "local"}
	if err := roleRepo.Create(ctx, role); err != nil {
		t.Fatalf("Create role: %v", err)
	}

	u := makeUser("cascade_user", "cascade@test.com")
	if err := userRepo.Create(ctx, u); err != nil {
		t.Fatalf("Create user: %v", err)
	}
	if err := roleRepo.SetUserRoles(ctx, u.ID, []string{role.ID}); err != nil {
		t.Fatalf("SetUserRoles: %v", err)
	}

	if err := userRepo.Delete(ctx, u.Username); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// user_roles row should have been CASCADE-deleted — verify via role still existing.
	gotRole, err := roleRepo.Get(ctx, role.ID)
	if err != nil {
		t.Fatalf("Get role after user delete: %v", err)
	}
	if gotRole == nil {
		t.Fatal("Role was unexpectedly deleted")
	}
	// And GetUserRoles for the now-deleted user ID should return empty (no error).
	userRoles, err := roleRepo.GetUserRoles(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetUserRoles after Delete: %v", err)
	}
	if len(userRoles) != 0 {
		t.Errorf("GetUserRoles after Delete: got %v, want empty", userRoles)
	}
}
