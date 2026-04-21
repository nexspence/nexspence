package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/auth"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
	"go.uber.org/zap"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockLDAP implements auth.LDAPAuthenticator for testing.
type mockLDAP struct {
	user *auth.LDAPUser
	err  error
	testErr error
}

func (m *mockLDAP) Authenticate(_ context.Context, _, _ string) (*auth.LDAPUser, error) {
	return m.user, m.err
}
func (m *mockLDAP) TestConnection(_ context.Context) error { return m.testErr }

func newUserSvcWithLDAP(ldap auth.LDAPAuthenticator) (*service.UserService, *testutil.UserRepo) {
	users := testutil.NewUserRepo()
	roles := testutil.NewRoleRepo()
	authSvc := auth.NewService("test-secret-32-chars-long-here!!", 1, 4)
	svc := service.NewUserService(users, roles, authSvc, zap.NewNop().Sugar()).WithLDAP(ldap, "")
	return svc, users
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
