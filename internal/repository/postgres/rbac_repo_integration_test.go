//go:build integration

package postgres

import (
	"context"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/testutil/pgtest"
)

// TestRBACRepo_GetUserPrivilegesWithSelectors_NoRoles returns empty for a user with no roles.
func TestRBACRepo_GetUserPrivilegesWithSelectors_NoRoles(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	userRepo := NewUserRepo(pool)
	rbacRepo := NewRBACRepo(pool)

	u := makeUser("rbac_noroles_user", "rbac_noroles@test.com")
	if err := userRepo.Create(ctx, u); err != nil {
		t.Fatalf("Create user: %v", err)
	}

	result, err := rbacRepo.GetUserPrivilegesWithSelectors(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetUserPrivilegesWithSelectors: %v", err)
	}
	if len(result) != 0 {
		t.Fatalf("expected empty slice, got %d: %+v", len(result), result)
	}
}

// TestRBACRepo_GetUserPrivilegesWithSelectors_UnknownUser returns empty for a nonexistent user.
func TestRBACRepo_GetUserPrivilegesWithSelectors_UnknownUser(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	rbacRepo := NewRBACRepo(pool)

	const missing = "00000000-0000-0000-0000-000000000000"
	result, err := rbacRepo.GetUserPrivilegesWithSelectors(ctx, missing)
	if err != nil {
		t.Fatalf("GetUserPrivilegesWithSelectors(missing): unexpected error: %v", err)
	}
	if len(result) != 0 {
		t.Fatalf("expected empty slice for unknown user, got %d", len(result))
	}
}

// TestRBACRepo_GetUserPrivilegesWithSelectors_FullChain verifies the full
// user → user_roles → role_privileges → privileges → content_selectors join.
func TestRBACRepo_GetUserPrivilegesWithSelectors_FullChain(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	userRepo := NewUserRepo(pool)
	roleRepo := NewRoleRepo(pool)
	privRepo := NewPrivilegeRepo(pool)
	csRepo := NewContentSelectorRepo(pool)
	rbacRepo := NewRBACRepo(pool)

	// Content selector with a specific CEL expression.
	cs := &domain.ContentSelector{
		Name:       "rbac_chain_cs",
		Expression: `format == "maven2" && path.startsWith("/com/acme/")`,
	}
	if err := csRepo.Create(ctx, cs); err != nil {
		t.Fatalf("Create content selector: %v", err)
	}

	// Privilege with actions in Attrs.
	priv := &domain.Privilege{
		Name:              "rbac_chain_priv",
		Type:              domain.PrivilegeTypeRepositoryContentSelector,
		Attrs:             map[string]any{"actions": []string{"read", "write"}},
		ContentSelectorID: &cs.ID,
	}
	if err := privRepo.Create(ctx, priv); err != nil {
		t.Fatalf("Create privilege: %v", err)
	}

	// Role linked to the privilege.
	role := makeRole("rbac_chain_role", "local")
	if err := roleRepo.Create(ctx, role); err != nil {
		t.Fatalf("Create role: %v", err)
	}
	if err := roleRepo.SetPrivileges(ctx, role.ID, []string{priv.ID}); err != nil {
		t.Fatalf("SetPrivileges: %v", err)
	}

	// User with the role.
	u := makeUser("rbac_chain_user", "rbac_chain@test.com")
	if err := userRepo.Create(ctx, u); err != nil {
		t.Fatalf("Create user: %v", err)
	}
	if err := roleRepo.SetUserRoles(ctx, u.ID, []string{role.ID}); err != nil {
		t.Fatalf("SetUserRoles: %v", err)
	}

	result, err := rbacRepo.GetUserPrivilegesWithSelectors(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetUserPrivilegesWithSelectors: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 PrivilegeWithSelector, got %d", len(result))
	}
	ps := result[0]

	if ps.Expression != cs.Expression {
		t.Errorf("Expression: got %q, want %q", ps.Expression, cs.Expression)
	}
	if len(ps.Actions) != 2 {
		t.Errorf("Actions: got %v, want [read write]", ps.Actions)
	}
}

// TestRBACRepo_GetUserPrivilegesWithSelectors_MultiplePrivileges verifies that
// all privileges (across multiple roles) are returned for a user.
func TestRBACRepo_GetUserPrivilegesWithSelectors_MultiplePrivileges(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	userRepo := NewUserRepo(pool)
	roleRepo := NewRoleRepo(pool)
	privRepo := NewPrivilegeRepo(pool)
	csRepo := NewContentSelectorRepo(pool)
	rbacRepo := NewRBACRepo(pool)

	cs1 := &domain.ContentSelector{Name: "rbac_multi_cs1", Expression: `format == "npm"`}
	cs2 := &domain.ContentSelector{Name: "rbac_multi_cs2", Expression: `format == "pypi"`}
	if err := csRepo.Create(ctx, cs1); err != nil {
		t.Fatalf("Create cs1: %v", err)
	}
	if err := csRepo.Create(ctx, cs2); err != nil {
		t.Fatalf("Create cs2: %v", err)
	}

	p1 := &domain.Privilege{
		Name: "rbac_multi_p1", Type: domain.PrivilegeTypeRepositoryContentSelector,
		Attrs: map[string]any{"actions": []string{"read"}}, ContentSelectorID: &cs1.ID,
	}
	p2 := &domain.Privilege{
		Name: "rbac_multi_p2", Type: domain.PrivilegeTypeRepositoryContentSelector,
		Attrs: map[string]any{"actions": []string{"write"}}, ContentSelectorID: &cs2.ID,
	}
	if err := privRepo.Create(ctx, p1); err != nil {
		t.Fatalf("Create p1: %v", err)
	}
	if err := privRepo.Create(ctx, p2); err != nil {
		t.Fatalf("Create p2: %v", err)
	}

	// Two roles, each with one privilege.
	r1 := makeRole("rbac_multi_r1", "local")
	r2 := makeRole("rbac_multi_r2", "local")
	if err := roleRepo.Create(ctx, r1); err != nil {
		t.Fatalf("Create r1: %v", err)
	}
	if err := roleRepo.Create(ctx, r2); err != nil {
		t.Fatalf("Create r2: %v", err)
	}
	if err := roleRepo.SetPrivileges(ctx, r1.ID, []string{p1.ID}); err != nil {
		t.Fatalf("SetPrivileges r1: %v", err)
	}
	if err := roleRepo.SetPrivileges(ctx, r2.ID, []string{p2.ID}); err != nil {
		t.Fatalf("SetPrivileges r2: %v", err)
	}

	// User with both roles.
	u := makeUser("rbac_multi_user", "rbac_multi@test.com")
	if err := userRepo.Create(ctx, u); err != nil {
		t.Fatalf("Create user: %v", err)
	}
	if err := roleRepo.SetUserRoles(ctx, u.ID, []string{r1.ID, r2.ID}); err != nil {
		t.Fatalf("SetUserRoles: %v", err)
	}

	result, err := rbacRepo.GetUserPrivilegesWithSelectors(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetUserPrivilegesWithSelectors: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 PrivilegeWithSelectors, got %d: %+v", len(result), result)
	}
}

// TestRBACRepo_GetUserPrivilegesWithSelectors_PrivWithoutSelector_ExcludedFromResult
// verifies that privileges where content_selector_id IS NULL are not returned
// (the query filters on p.content_selector_id IS NOT NULL).
// Note: migration 007 prevents creating repository-content-selector type without a selector,
// but other types can have NULL. We insert directly to test the filtering logic.
func TestRBACRepo_GetUserPrivilegesWithSelectors_NullSelectorExcluded(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "roles", "privileges", "content_selectors")
	ctx := context.Background()
	userRepo := NewUserRepo(pool)
	roleRepo := NewRoleRepo(pool)
	rbacRepo := NewRBACRepo(pool)

	// Insert a wildcard privilege with NULL content_selector_id directly.
	var privID string
	err := pool.QueryRow(ctx,
		`INSERT INTO privileges (name, description, type, attrs, content_selector_id)
		 VALUES ($1, $2, $3, $4, NULL) RETURNING id`,
		"rbac_null_sel_priv", "", "wildcard", `{}`,
	).Scan(&privID)
	if err != nil {
		t.Fatalf("Insert wildcard privilege: %v", err)
	}

	role := makeRole("rbac_null_sel_role", "local")
	if err := roleRepo.Create(ctx, role); err != nil {
		t.Fatalf("Create role: %v", err)
	}
	if err := roleRepo.SetPrivileges(ctx, role.ID, []string{privID}); err != nil {
		t.Fatalf("SetPrivileges: %v", err)
	}

	u := makeUser("rbac_null_sel_user", "rbac_null_sel@test.com")
	if err := userRepo.Create(ctx, u); err != nil {
		t.Fatalf("Create user: %v", err)
	}
	if err := roleRepo.SetUserRoles(ctx, u.ID, []string{role.ID}); err != nil {
		t.Fatalf("SetUserRoles: %v", err)
	}

	result, err := rbacRepo.GetUserPrivilegesWithSelectors(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetUserPrivilegesWithSelectors: %v", err)
	}
	// The wildcard priv has NULL selector so must be excluded.
	if len(result) != 0 {
		t.Errorf("expected 0 results (null selector excluded), got %d: %+v", len(result), result)
	}
}
