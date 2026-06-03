package service

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/nexspence-oss/nexspence/internal/auth"
	"github.com/nexspence-oss/nexspence/internal/config"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// mockSAML satisfies auth.SAMLAuthenticator. LoginSAML never calls back into it.
type mockSAML struct{}

func (m *mockSAML) MetadataXML() ([]byte, error)                            { return nil, nil } //nolint:nilnil // not-found stub; callers check the returned value
func (m *mockSAML) AuthnRequestURL(rs string) (string, error)               { return "https://idp/sso", nil }
func (m *mockSAML) ParseResponse(r *http.Request) (*auth.SAMLClaims, error) { return nil, nil } //nolint:nilnil // not-found stub; callers check the returned value
func (m *mockSAML) SignRelayState(returnTo string) string                   { return returnTo }
func (m *mockSAML) VerifyRelayState(rs string) (string, error)              { return rs, nil }

func newUserSvcSAML(t *testing.T, cfg config.SAMLConfig, seed ...*domain.User) *UserService {
	t.Helper()
	users := testutil.NewUserRepo(seed...)
	roles := testutil.NewRoleRepo(
		&domain.Role{ID: "role-admin", Name: "nx-admin"},
		&domain.Role{ID: "role-release", Name: "release-manager"},
		&domain.Role{ID: "role-read", Name: "read-only"},
	)
	authSvc := auth.NewService("test-secret-saml123", 24, 4)
	s := NewUserService(users, roles, authSvc, zap.NewNop().Sugar())
	return s.WithSAML(&mockSAML{}, cfg)
}

func baseSAMLCfg() config.SAMLConfig {
	return config.SAMLConfig{
		Enabled:      true,
		Provisioning: "jit",
		AdminGroup:   "nexspence-admins",
		RoleMappings: map[string]string{"developers": "release-manager"},
	}
}

func TestLoginSAML_NewUser_JIT_AutoCreatesWithRoles(t *testing.T) {
	s := newUserSvcSAML(t, baseSAMLCfg())
	claims := &auth.SAMLClaims{
		Subject:  "alice@idp",
		Username: "alice",
		Email:    "alice@ex.com",
		Name:     "Alice Example",
		Groups:   []string{"developers", "nexspence-admins"},
	}
	tok, u, err := s.LoginSAML(context.Background(), claims)
	require.NoError(t, err)
	assert.NotEmpty(t, tok)
	assert.Equal(t, "alice", u.Username)
	assert.Equal(t, domain.UserSourceSAML, u.Source)
	assert.ElementsMatch(t, []string{"nx-admin", "release-manager"}, u.Roles)
}

func TestLoginSAML_NewUser_Allowlist_EmailMatch_Created(t *testing.T) {
	cfg := baseSAMLCfg()
	cfg.Provisioning = "allowlist"
	cfg.EmailAllowlist = []string{"*@company.com"}
	s := newUserSvcSAML(t, cfg)
	_, u, err := s.LoginSAML(context.Background(), &auth.SAMLClaims{
		Username: "bob", Email: "bob@company.com",
		Groups: []string{"developers"},
	})
	require.NoError(t, err)
	assert.Equal(t, "bob", u.Username)
	assert.Contains(t, u.Roles, "release-manager")
}

func TestLoginSAML_NewUser_Allowlist_EmailMiss_Rejected(t *testing.T) {
	cfg := baseSAMLCfg()
	cfg.Provisioning = "allowlist"
	cfg.EmailAllowlist = []string{"*@company.com"}
	s := newUserSvcSAML(t, cfg)
	_, _, err := s.LoginSAML(context.Background(), &auth.SAMLClaims{
		Username: "mallory", Email: "mallory@evil.io",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrProvisioningRejected))
}

func TestLoginSAML_NewUser_Manual_Rejected(t *testing.T) {
	cfg := baseSAMLCfg()
	cfg.Provisioning = "manual"
	s := newUserSvcSAML(t, cfg)
	_, _, err := s.LoginSAML(context.Background(), &auth.SAMLClaims{
		Username: "alice", Email: "alice@ex.com",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrProvisioningRejected))
}

func TestLoginSAML_ExistingUser_SourceMismatch_Rejected(t *testing.T) {
	existing := &domain.User{
		ID:       "u1",
		Username: "alice",
		Email:    "alice@ex.com",
		Source:   domain.UserSourceLocal,
		Status:   domain.UserStatusActive,
	}
	s := newUserSvcSAML(t, baseSAMLCfg(), existing)
	_, _, err := s.LoginSAML(context.Background(), &auth.SAMLClaims{
		Username: "alice", Email: "alice@ex.com",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrProvisioningConflict))
}

func TestLoginSAML_ExistingSAMLUser_SyncRoles_Replaces(t *testing.T) {
	existing := &domain.User{
		ID:       "u1",
		Username: "alice",
		Email:    "alice@ex.com",
		Source:   domain.UserSourceSAML,
		Status:   domain.UserStatusActive,
	}
	s := newUserSvcSAML(t, baseSAMLCfg(), existing)
	require.NoError(t, s.roles.SetUserRoles(context.Background(), "u1", []string{"role-admin"}))

	_, u, err := s.LoginSAML(context.Background(), &auth.SAMLClaims{
		Username: "alice", Email: "alice@ex.com",
		Groups: []string{"developers"},
	})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"release-manager"}, u.Roles)
}

func TestLoginSAML_AdminGroup_AssignsNxAdmin(t *testing.T) {
	s := newUserSvcSAML(t, baseSAMLCfg())
	_, u, err := s.LoginSAML(context.Background(), &auth.SAMLClaims{
		Username: "alice", Email: "alice@ex.com",
		Groups: []string{"nexspence-admins"},
	})
	require.NoError(t, err)
	assert.Contains(t, u.Roles, "nx-admin")
}

func TestLoginSAML_InactiveUser_Rejected(t *testing.T) {
	existing := &domain.User{
		ID:       "u1",
		Username: "alice",
		Email:    "alice@ex.com",
		Source:   domain.UserSourceSAML,
		Status:   domain.UserStatusDisabled,
	}
	s := newUserSvcSAML(t, baseSAMLCfg(), existing)
	_, _, err := s.LoginSAML(context.Background(), &auth.SAMLClaims{
		Username: "alice", Email: "alice@ex.com",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalidInput))
}

func TestLoginSAML_EmptyUsername_Rejected(t *testing.T) {
	s := newUserSvcSAML(t, baseSAMLCfg())
	_, _, err := s.LoginSAML(context.Background(), &auth.SAMLClaims{
		Username: "", Email: "alice@ex.com",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalidInput))
}

func TestLoginSAML_SAMLDisabled_Fails(t *testing.T) {
	users := testutil.NewUserRepo()
	roles := testutil.NewRoleRepo()
	authSvc := auth.NewService("secret-saml-off", 24, 4)
	s := NewUserService(users, roles, authSvc, zap.NewNop().Sugar())
	// Not calling WithSAML.
	_, _, err := s.LoginSAML(context.Background(), &auth.SAMLClaims{
		Username: "a", Email: "a@b.com",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalidInput))
}
