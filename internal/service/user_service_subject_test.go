package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/nexspence-oss/nexspence/internal/auth"
	"github.com/nexspence-oss/nexspence/internal/config"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// newUserSvcOIDCWithUsers builds the service over a user repo the test keeps
// a handle on, so it can read back what a login recorded.
func newUserSvcOIDCWithUsers(t *testing.T, users *testutil.UserRepo, cfg config.OIDCConfig) *UserService {
	t.Helper()
	roles := testutil.NewRoleRepo(
		&domain.Role{ID: "role-admin", Name: "nx-admin"},
		&domain.Role{ID: "role-release", Name: "release-manager"},
		&domain.Role{ID: "role-read", Name: "read-only"},
	)
	authSvc := auth.NewService("test-secret-abcdef0123", 24, 4)
	s := NewUserService(users, roles, authSvc, zap.NewNop().Sugar())
	return s.WithOIDC(&mockOIDC{}, cfg)
}

// newUserSvcSAMLWithUsers is the SAML counterpart.
func newUserSvcSAMLWithUsers(t *testing.T, users *testutil.UserRepo, cfg config.SAMLConfig) *UserService {
	t.Helper()
	roles := testutil.NewRoleRepo(
		&domain.Role{ID: "role-admin", Name: "nx-admin"},
		&domain.Role{ID: "role-release", Name: "release-manager"},
		&domain.Role{ID: "role-read", Name: "read-only"},
	)
	authSvc := auth.NewService("test-secret-saml123", 24, 4)
	s := NewUserService(users, roles, authSvc, zap.NewNop().Sugar())
	return s.WithSAML(&mockSAML{}, cfg)
}

// The username is not the identity: an IdP may hand the same
// preferred_username to a different principal — the employee left, the
// address was recycled, the directory was renamed. The immutable subject
// (OIDC `sub`, SAML NameID) is what identifies the principal, so an account
// that has one must not be handed to a login carrying another.
func TestLoginOIDC_SameUsernameNewSubject_Refused(t *testing.T) {
	s := newUserSvcOIDC(t, baseOIDCSvcCfg(), &domain.User{
		ID: "u-1", Username: "alice", Email: "alice@ex.com",
		Source: domain.UserSourceOIDC, Status: domain.UserStatusActive,
		ExternalID: "sub-original",
	})

	_, _, err := s.LoginOIDC(context.Background(), &auth.OIDCClaims{
		Subject: "sub-new", Username: "alice", Email: "alice@ex.com",
	}, "fake-id-token")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrProvisioningConflict)
}

// The same subject logging in again is the same person.
func TestLoginOIDC_SameSubject_Accepted(t *testing.T) {
	s := newUserSvcOIDC(t, baseOIDCSvcCfg(), &domain.User{
		ID: "u-1", Username: "alice", Email: "alice@ex.com",
		Source: domain.UserSourceOIDC, Status: domain.UserStatusActive,
		ExternalID: "sub-original",
	})

	tok, u, err := s.LoginOIDC(context.Background(), &auth.OIDCClaims{
		Subject: "sub-original", Username: "alice", Email: "alice@ex.com",
	}, "fake-id-token")
	require.NoError(t, err)
	assert.NotEmpty(t, tok)
	assert.Equal(t, "alice", u.Username)
}

// The first login binds the subject to the account, so the next one can be
// checked against it.
func TestLoginOIDC_FirstLogin_BindsSubject(t *testing.T) {
	users := testutil.NewUserRepo()
	s := newUserSvcOIDCWithUsers(t, users, baseOIDCSvcCfg())

	_, _, err := s.LoginOIDC(context.Background(), &auth.OIDCClaims{
		Subject: "sub-1", Username: "alice", Email: "alice@ex.com",
	}, "fake-id-token")
	require.NoError(t, err)

	stored, err := users.Get(context.Background(), "alice")
	require.NoError(t, err)
	assert.Equal(t, "sub-1", stored.ExternalID, "the first login must bind the subject to the account")
}

// SAML carries the same immutable identifier in the NameID.
func TestLoginSAML_SameUsernameNewSubject_Refused(t *testing.T) {
	s := newUserSvcSAML(t, baseSAMLCfg(), &domain.User{
		ID: "u-1", Username: "alice", Email: "alice@ex.com",
		Source: domain.UserSourceSAML, Status: domain.UserStatusActive,
		ExternalID: "nameid-original",
	})

	_, _, err := s.LoginSAML(context.Background(), &auth.SAMLClaims{
		Subject: "nameid-new", Username: "alice", Email: "alice@ex.com",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrProvisioningConflict)
}

func TestLoginSAML_FirstLogin_BindsSubject(t *testing.T) {
	users := testutil.NewUserRepo()
	s := newUserSvcSAMLWithUsers(t, users, baseSAMLCfg())

	_, _, err := s.LoginSAML(context.Background(), &auth.SAMLClaims{
		Subject: "nameid-1", Username: "alice", Email: "alice@ex.com",
	})
	require.NoError(t, err)

	stored, err := users.Get(context.Background(), "alice")
	require.NoError(t, err)
	assert.Equal(t, "nameid-1", stored.ExternalID, "the first login must bind the subject to the account")
}

// An account provisioned before the subject was recorded has nothing to
// compare against. Binding it on the next login keeps the instance usable
// after the upgrade, and the check applies from then on.
func TestLoginOIDC_LegacyAccountWithoutSubject_BindsOnNextLogin(t *testing.T) {
	s := newUserSvcOIDC(t, baseOIDCSvcCfg(), &domain.User{
		ID: "u-1", Username: "alice", Email: "alice@ex.com",
		Source: domain.UserSourceOIDC, Status: domain.UserStatusActive,
	})

	_, u, err := s.LoginOIDC(context.Background(), &auth.OIDCClaims{
		Subject: "sub-1", Username: "alice", Email: "alice@ex.com",
	}, "fake-id-token")
	require.NoError(t, err)
	assert.Equal(t, "sub-1", u.ExternalID)
}

// A login without a subject cannot be bound to anything, and must not fail a
// user the IdP identifies some other way.
func TestLoginOIDC_WithoutSubject_StillLogsIn(t *testing.T) {
	s := newUserSvcOIDC(t, baseOIDCSvcCfg())
	_, u, err := s.LoginOIDC(context.Background(), &auth.OIDCClaims{
		Username: "alice", Email: "alice@ex.com",
	}, "fake-id-token")
	require.NoError(t, err)
	assert.Equal(t, "alice", u.Username)
}
