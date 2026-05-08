package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/auth"
	"github.com/nexspence-oss/nexspence/internal/config"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
	"go.uber.org/zap"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockLDAP implements auth.LDAPAuthenticator for testing.
type mockLDAP struct {
	user    *auth.LDAPUser
	err     error
	testErr error
}

func (m *mockLDAP) Authenticate(_ context.Context, _, _ string) (*auth.LDAPUser, error) {
	return m.user, m.err
}
func (m *mockLDAP) TestConnection(_ context.Context) error { return m.testErr }

// newUserSvcWithLDAP creates a UserService with LDAP, no config (admin group empty).
func newUserSvcWithLDAP(ldap auth.LDAPAuthenticator) (*service.UserService, *testutil.UserRepo) {
	users := testutil.NewUserRepo()
	roles := testutil.NewRoleRepo()
	authSvc := auth.NewService("test-secret-32-chars-long-here!!", 1, 4)
	svc := service.NewUserService(users, roles, authSvc, zap.NewNop().Sugar()).
		WithLDAP(ldap, config.LDAPConfig{})
	return svc, users
}

// newUserSvcWithLDAPConfig creates a UserService with LDAP config and pre-seeded roles.
func newUserSvcWithLDAPConfig(ldap auth.LDAPAuthenticator, cfg config.LDAPConfig, roles ...*domain.Role) (*service.UserService, *testutil.UserRepo, *testutil.RoleRepo) {
	userRepo := testutil.NewUserRepo()
	roleRepo := testutil.NewRoleRepo(roles...)
	authSvc := auth.NewService("test-secret-32-chars-long-here!!", 1, 4)
	svc := service.NewUserService(userRepo, roleRepo, authSvc, zap.NewNop().Sugar()).
		WithLDAP(ldap, cfg)
	return svc, userRepo, roleRepo
}

func TestLogin_LDAPSuccess_NewUser(t *testing.T) {
	mock := &mockLDAP{user: &auth.LDAPUser{
		Username: "alice", Email: "alice@corp.com",
		FirstName: "Alice", LastName: "Smith",
		Groups: []string{"developers"},
	}}
	svc, users := newUserSvcWithLDAP(mock)

	token, u, err := svc.Login(context.Background(), "alice", "secret")
	require.NoError(t, err)
	assert.NotEmpty(t, token)
	assert.Equal(t, "alice", u.Username)
	assert.Equal(t, domain.UserSourceLDAP, u.Source)

	// User should be auto-created in local DB.
	created, _ := users.Get(context.Background(), "alice")
	require.NotNil(t, created)
	assert.Equal(t, "alice@corp.com", created.Email)
}

func TestLogin_LDAPSuccess_ExistingUser_ProfileSynced(t *testing.T) {
	mock := &mockLDAP{user: &auth.LDAPUser{
		Username: "bob", Email: "bob@new.com", FirstName: "Robert",
	}}
	svc, users := newUserSvcWithLDAP(mock)

	// Pre-create user as LDAP type with old email.
	existing := &domain.User{
		Username: "bob", Email: "bob@old.com", FirstName: "Bob",
		Status: domain.UserStatusActive, Source: domain.UserSourceLDAP,
	}
	require.NoError(t, users.Create(context.Background(), existing))

	_, u, err := svc.Login(context.Background(), "bob", "pass")
	require.NoError(t, err)
	assert.Equal(t, "bob@new.com", u.Email)
	assert.Equal(t, "Robert", u.FirstName)
}

func TestLogin_LDAPFail_WrongPassword(t *testing.T) {
	mock := &mockLDAP{err: errors.New("invalid credentials")}
	svc, _ := newUserSvcWithLDAP(mock)

	_, _, err := svc.Login(context.Background(), "carol", "wrong")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "LDAP authentication failed")
}

func TestLogin_LDAPDisabled_FallsBackToLocal(t *testing.T) {
	users := testutil.NewUserRepo()
	roles := testutil.NewRoleRepo()
	authSvc := auth.NewService("test-secret-32-chars-long-here!!", 1, 4)
	svc := service.NewUserService(users, roles, authSvc, zap.NewNop().Sugar()) // no LDAP

	hash, _ := authSvc.HashPassword("mypass")
	u := &domain.User{
		Username: "dave", Status: domain.UserStatusActive,
		Source: domain.UserSourceLocal,
	}
	u.PasswordHash = hash
	require.NoError(t, users.Create(context.Background(), u))

	token, _, err := svc.Login(context.Background(), "dave", "mypass")
	require.NoError(t, err)
	assert.NotEmpty(t, token)
}

func TestLogin_LDAP_DisabledUser_Rejected(t *testing.T) {
	mock := &mockLDAP{user: &auth.LDAPUser{Username: "eve"}}
	svc, users := newUserSvcWithLDAP(mock)

	disabled := &domain.User{
		Username: "eve", Status: domain.UserStatusDisabled,
		Source: domain.UserSourceLDAP,
	}
	require.NoError(t, users.Create(context.Background(), disabled))

	_, _, err := svc.Login(context.Background(), "eve", "pass")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not active")
}

// ── Phase 60: LDAP External Role Mapping ──────────────────────────────────────

func TestLogin_LDAP_GroupNameMatchesRole(t *testing.T) {
	// LDAP user in group "developers" should get the role named "developers".
	devRole := &domain.Role{ID: "role-dev", Name: "developers"}
	mock := &mockLDAP{user: &auth.LDAPUser{
		Username: "frank", Email: "frank@corp.com",
		Groups: []string{"developers"},
	}}
	svc, _, roleRepo := newUserSvcWithLDAPConfig(mock, config.LDAPConfig{}, devRole)

	_, u, err := svc.Login(context.Background(), "frank", "pass")
	require.NoError(t, err)
	assert.Contains(t, u.Roles, "developers")

	userRoles, _ := roleRepo.GetUserRoles(context.Background(), u.ID)
	roleNames := make([]string, 0, len(userRoles))
	for _, r := range userRoles {
		roleNames = append(roleNames, r.Name)
	}
	assert.Contains(t, roleNames, "developers")
}

func TestLogin_LDAP_AdminGroupGrantsNxAdmin(t *testing.T) {
	// LDAP user in the configured admin_group gets the nx-admin role.
	adminRole := &domain.Role{ID: "role-admin", Name: "nx-admin"}
	mock := &mockLDAP{user: &auth.LDAPUser{
		Username: "grace", Email: "grace@corp.com",
		Groups: []string{"infra-admins"},
	}}
	cfg := config.LDAPConfig{AdminGroup: "infra-admins"}
	svc, _, roleRepo := newUserSvcWithLDAPConfig(mock, cfg, adminRole)

	_, u, err := svc.Login(context.Background(), "grace", "pass")
	require.NoError(t, err)
	assert.Contains(t, u.Roles, "nx-admin")

	userRoles, _ := roleRepo.GetUserRoles(context.Background(), u.ID)
	roleNames := make([]string, 0, len(userRoles))
	for _, r := range userRoles {
		roleNames = append(roleNames, r.Name)
	}
	assert.Contains(t, roleNames, "nx-admin")
}

func TestLogin_LDAP_RoleMappingsConfig(t *testing.T) {
	// LDAP group "dev-team" mapped via role_mappings to role "developers".
	devRole := &domain.Role{ID: "role-dev", Name: "developers"}
	mock := &mockLDAP{user: &auth.LDAPUser{
		Username: "henry", Email: "henry@corp.com",
		Groups: []string{"dev-team"},
	}}
	cfg := config.LDAPConfig{
		RoleMappings: map[string]string{"dev-team": "developers"},
	}
	svc, _, roleRepo := newUserSvcWithLDAPConfig(mock, cfg, devRole)

	_, u, err := svc.Login(context.Background(), "henry", "pass")
	require.NoError(t, err)
	assert.Contains(t, u.Roles, "developers")

	userRoles, _ := roleRepo.GetUserRoles(context.Background(), u.ID)
	roleNames := make([]string, 0, len(userRoles))
	for _, r := range userRoles {
		roleNames = append(roleNames, r.Name)
	}
	assert.Contains(t, roleNames, "developers")
}

func TestLogin_LDAP_ReplaceSemantics(t *testing.T) {
	// LDAP user previously had "old-role". On next login with group "developers",
	// REPLACE semantics: old-role is removed, developers is set.
	oldRole := &domain.Role{ID: "role-old", Name: "old-role"}
	devRole := &domain.Role{ID: "role-dev", Name: "developers"}
	mock := &mockLDAP{user: &auth.LDAPUser{
		Username: "ivan", Email: "ivan@corp.com",
		Groups: []string{"developers"},
	}}
	svc, userRepo, roleRepo := newUserSvcWithLDAPConfig(mock, config.LDAPConfig{}, oldRole, devRole)

	// Pre-create user with old-role assigned.
	existing := &domain.User{
		Username: "ivan", Email: "ivan@corp.com",
		Status: domain.UserStatusActive, Source: domain.UserSourceLDAP,
	}
	require.NoError(t, userRepo.Create(context.Background(), existing))
	require.NoError(t, roleRepo.SetUserRoles(context.Background(), existing.ID, []string{"role-old"}))

	_, u, err := svc.Login(context.Background(), "ivan", "pass")
	require.NoError(t, err)

	userRoles, _ := roleRepo.GetUserRoles(context.Background(), u.ID)
	roleNames := make([]string, 0, len(userRoles))
	for _, r := range userRoles {
		roleNames = append(roleNames, r.Name)
	}
	assert.Contains(t, roleNames, "developers")
	assert.NotContains(t, roleNames, "old-role", "REPLACE semantics: old-role should be removed")
}

func TestLogin_LDAP_NewUser_DoesNotHaveGroupsAsRoles(t *testing.T) {
	// Regression: Roles field in users.Create must NOT be set to lu.Groups.
	// That field doesn't write to user_roles table and was a stale attempt.
	mock := &mockLDAP{user: &auth.LDAPUser{
		Username: "julia", Email: "julia@corp.com",
		Groups: []string{"some-ldap-group"},
	}}
	svc, userRepo := newUserSvcWithLDAP(mock)

	_, _, err := svc.Login(context.Background(), "julia", "pass")
	require.NoError(t, err)

	created, _ := userRepo.Get(context.Background(), "julia")
	require.NotNil(t, created)
	// Roles on the User struct should not be populated from LDAP groups
	// (those are not DB-backed role assignments).
	assert.Empty(t, created.Roles, "user.Roles should not be set from LDAP Groups at create time")
}
