package service

import (
	"context"
	"errors"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/auth"
	"github.com/nexspence-oss/nexspence/internal/config"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// mockOIDC satisfies auth.OIDCAuthenticator. LoginOIDC never calls back into
// it during these tests — we drive it directly with canned claims.
type mockOIDC struct{}

func (m *mockOIDC) AuthCodeURL(state, nonce, cc string) string { return "" }
func (m *mockOIDC) ExchangeAndVerify(ctx context.Context, code, v, n string) (*auth.OIDCClaims, string, error) {
	return &auth.OIDCClaims{
		Subject:  "sub-1",
		Username: "alice",
		Email:    "alice@example.com",
	}, "fake-id-token", nil
}
func (m *mockOIDC) TestConnection(ctx context.Context) error { return nil }
func (m *mockOIDC) EndSessionEndpoint() string { return "" }

func newUserSvcOIDC(t *testing.T, cfg config.OIDCConfig, seed ...*domain.User) *UserService {
	t.Helper()
	users := testutil.NewUserRepo(seed...)
	roles := testutil.NewRoleRepo(
		&domain.Role{ID: "role-admin", Name: "nx-admin"},
		&domain.Role{ID: "role-release", Name: "release-manager"},
		&domain.Role{ID: "role-read", Name: "read-only"},
	)
	authSvc := auth.NewService("test-secret-abcdef0123", 24, 4)
	s := NewUserService(users, roles, authSvc, zap.NewNop().Sugar())
	return s.WithOIDC(&mockOIDC{}, cfg)
}

func baseOIDCSvcCfg() config.OIDCConfig {
	return config.OIDCConfig{
		Enabled:      true,
		Provisioning: "jit",
		AdminGroup:   "nexspense-admins",
		RoleMappings: map[string]string{"developers": "release-manager"},
	}
}

func TestLoginOIDC_NewUser_JIT_AutoCreatesWithRoles(t *testing.T) {
	s := newUserSvcOIDC(t, baseOIDCSvcCfg())
	claims := &auth.OIDCClaims{
		Username:  "alice",
		Email:     "alice@ex.com",
		FirstName: "Alice",
		LastName:  "Example",
		Groups:    []string{"developers", "nexspense-admins"},
	}
	tok, u, err := s.LoginOIDC(context.Background(), claims, "fake-id-token")
	require.NoError(t, err)
	assert.NotEmpty(t, tok)
	assert.Equal(t, "alice", u.Username)
	assert.Equal(t, domain.UserSourceOIDC, u.Source)
	assert.ElementsMatch(t, []string{"nx-admin", "release-manager"}, u.Roles)
}

func TestLoginOIDC_NewUser_Allowlist_EmailMatch_Created(t *testing.T) {
	cfg := baseOIDCSvcCfg()
	cfg.Provisioning = "allowlist"
	cfg.EmailAllowlist = []string{"*@company.com"}
	s := newUserSvcOIDC(t, cfg)
	_, u, err := s.LoginOIDC(context.Background(), &auth.OIDCClaims{
		Username: "bob", Email: "bob@company.com",
		Groups: []string{"developers"},
	}, "fake-id-token")
	require.NoError(t, err)
	assert.Equal(t, "bob", u.Username)
	assert.Contains(t, u.Roles, "release-manager")
}

func TestLoginOIDC_NewUser_Allowlist_EmailMiss_Rejected(t *testing.T) {
	cfg := baseOIDCSvcCfg()
	cfg.Provisioning = "allowlist"
	cfg.EmailAllowlist = []string{"*@company.com"}
	s := newUserSvcOIDC(t, cfg)
	_, _, err := s.LoginOIDC(context.Background(), &auth.OIDCClaims{
		Username: "mallory", Email: "mallory@evil.io",
	}, "fake-id-token")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrProvisioningRejected))
}

func TestLoginOIDC_NewUser_Manual_Rejected(t *testing.T) {
	cfg := baseOIDCSvcCfg()
	cfg.Provisioning = "manual"
	s := newUserSvcOIDC(t, cfg)
	_, _, err := s.LoginOIDC(context.Background(), &auth.OIDCClaims{
		Username: "alice", Email: "alice@ex.com",
	}, "fake-id-token")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrProvisioningRejected))
}

func TestLoginOIDC_ExistingUser_SourceMismatch_Rejected(t *testing.T) {
	existing := &domain.User{
		ID:       "u1",
		Username: "alice",
		Email:    "alice@ex.com",
		Source:   domain.UserSourceLocal,
		Status:   domain.UserStatusActive,
	}
	s := newUserSvcOIDC(t, baseOIDCSvcCfg(), existing)
	_, _, err := s.LoginOIDC(context.Background(), &auth.OIDCClaims{
		Username: "alice", Email: "alice@ex.com",
	}, "fake-id-token")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrProvisioningConflict))
}

func TestLoginOIDC_ExistingOIDCUser_SyncRoles_Replaces(t *testing.T) {
	existing := &domain.User{
		ID:       "u1",
		Username: "alice",
		Email:    "alice@ex.com",
		Source:   domain.UserSourceOIDC,
		Status:   domain.UserStatusActive,
	}
	s := newUserSvcOIDC(t, baseOIDCSvcCfg(), existing)
	// Pre-seed user with nx-admin (as if granted manually once).
	require.NoError(t, s.roles.SetUserRoles(context.Background(), "u1", []string{"role-admin"}))

	_, u, err := s.LoginOIDC(context.Background(), &auth.OIDCClaims{
		Username: "alice", Email: "alice@ex.com",
		Groups: []string{"developers"}, // no nexspense-admins → nx-admin must drop
	}, "fake-id-token")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"release-manager"}, u.Roles)
}

func TestLoginOIDC_MissingRoleInDB_Warns_NoFail(t *testing.T) {
	cfg := baseOIDCSvcCfg()
	cfg.RoleMappings = map[string]string{"developers": "role-that-does-not-exist"}
	s := newUserSvcOIDC(t, cfg)
	_, u, err := s.LoginOIDC(context.Background(), &auth.OIDCClaims{
		Username: "alice", Email: "alice@ex.com",
		Groups: []string{"developers"},
	}, "fake-id-token")
	require.NoError(t, err)
	assert.Empty(t, u.Roles)
}

func TestLoginOIDC_InactiveUser_Rejected(t *testing.T) {
	existing := &domain.User{
		ID:       "u1",
		Username: "alice",
		Email:    "alice@ex.com",
		Source:   domain.UserSourceOIDC,
		Status:   domain.UserStatusDisabled,
	}
	s := newUserSvcOIDC(t, baseOIDCSvcCfg(), existing)
	_, _, err := s.LoginOIDC(context.Background(), &auth.OIDCClaims{
		Username: "alice", Email: "alice@ex.com",
	}, "fake-id-token")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalidInput))
}

func TestLoginOIDC_EmptyUsername_Rejected(t *testing.T) {
	s := newUserSvcOIDC(t, baseOIDCSvcCfg())
	_, _, err := s.LoginOIDC(context.Background(), &auth.OIDCClaims{
		Username: "", Email: "alice@ex.com",
	}, "fake-id-token")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalidInput))
}

func TestLoginOIDC_DNFormatGroup_MatchesAdminGroup(t *testing.T) {
	s := newUserSvcOIDC(t, baseOIDCSvcCfg())
	_, u, err := s.LoginOIDC(context.Background(), &auth.OIDCClaims{
		Username: "alice", Email: "alice@ex.com",
		Groups: []string{"CN=nexspense-admins,OU=Groups,DC=ex,DC=com"},
	}, "fake-id-token")
	require.NoError(t, err)
	assert.Contains(t, u.Roles, "nx-admin")
}

func TestLoginOIDC_OIDCDisabled_Fails(t *testing.T) {
	users := testutil.NewUserRepo()
	roles := testutil.NewRoleRepo()
	authSvc := auth.NewService("secret-abc", 24, 4)
	s := NewUserService(users, roles, authSvc, zap.NewNop().Sugar())
	// Not calling WithOIDC.
	_, _, err := s.LoginOIDC(context.Background(), &auth.OIDCClaims{
		Username: "a", Email: "a@b.com",
	}, "fake-id-token")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalidInput))
}
