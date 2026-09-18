package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/auth"
	"github.com/nexspence-oss/nexspence/internal/domain"
)

// fakeGroupLookup stands in for the Google Directory client: LoginOIDC asks
// it for the user's groups when the id_token carries none.
type fakeGroupLookup struct {
	groups   []string
	err      error
	askedFor string
}

func (f *fakeGroupLookup) Groups(_ context.Context, email string) ([]string, error) {
	f.askedFor = email
	return f.groups, f.err
}

func TestLoginOIDC_GroupLookup_FillsGroupsFromDirectory(t *testing.T) {
	// Google never puts groups in the id_token; the directory answer must
	// feed the same admin_group / role_mappings pipeline a claim would.
	cfg := baseOIDCSvcCfg()
	cfg.AdminGroup = "nexspence-admins@company.com"
	cfg.RoleMappings = map[string]string{"developers@company.com": "release-manager"}
	lookup := &fakeGroupLookup{groups: []string{"developers@company.com", "nexspence-admins@company.com"}}
	s := newUserSvcOIDC(t, cfg).WithGroupLookup(lookup)

	_, u, err := s.LoginOIDC(context.Background(), &auth.OIDCClaims{
		Username: "alice", Email: "alice@company.com", // no Groups, GroupsPresent=false
	}, "fake-id-token")
	require.NoError(t, err)
	assert.Equal(t, "alice@company.com", lookup.askedFor)
	assert.ElementsMatch(t, []string{"nx-admin", "release-manager"}, u.Roles)
}

func TestLoginOIDC_GroupLookup_EmptyAnswerReplacesRoles(t *testing.T) {
	// A successful lookup that finds no groups is a confirmed answer:
	// REPLACE semantics drop the stale role, same as an empty claim.
	existing := &domain.User{
		ID: "u1", Username: "alice", Email: "alice@company.com",
		Source: domain.UserSourceOIDC, Status: domain.UserStatusActive,
	}
	s := newUserSvcOIDC(t, baseOIDCSvcCfg(), existing).WithGroupLookup(&fakeGroupLookup{})
	require.NoError(t, s.roles.SetUserRoles(context.Background(), "u1", []string{"role-admin"}))

	_, u, err := s.LoginOIDC(context.Background(), &auth.OIDCClaims{
		Username: "alice", Email: "alice@company.com",
	}, "fake-id-token")
	require.NoError(t, err)
	assert.Empty(t, u.Roles)
}

func TestLoginOIDC_GroupLookup_FailureKeepsExistingRoles(t *testing.T) {
	// A Directory API hiccup must not lock anyone out or strip their roles:
	// the login succeeds and the manually-assigned role survives.
	existing := &domain.User{
		ID: "u1", Username: "alice", Email: "alice@company.com",
		Source: domain.UserSourceOIDC, Status: domain.UserStatusActive,
	}
	lookup := &fakeGroupLookup{err: errors.New("directory: 503 backend error")}
	s := newUserSvcOIDC(t, baseOIDCSvcCfg(), existing).WithGroupLookup(lookup)
	require.NoError(t, s.roles.SetUserRoles(context.Background(), "u1", []string{"role-admin"}))

	tok, u, err := s.LoginOIDC(context.Background(), &auth.OIDCClaims{
		Username: "alice", Email: "alice@company.com",
	}, "fake-id-token")
	require.NoError(t, err)
	assert.NotEmpty(t, tok)
	assert.ElementsMatch(t, []string{"nx-admin"}, u.Roles)
}

func TestLoginOIDC_GroupLookup_OverridesTokenGroups(t *testing.T) {
	// When a lookup is configured it is the source of truth even if the
	// token happened to carry a groups claim.
	cfg := baseOIDCSvcCfg()
	lookup := &fakeGroupLookup{groups: []string{"developers"}}
	s := newUserSvcOIDC(t, cfg).WithGroupLookup(lookup)

	_, u, err := s.LoginOIDC(context.Background(), &auth.OIDCClaims{
		Username: "alice", Email: "alice@company.com",
		Groups: []string{"nexspence-admins"}, GroupsPresent: true,
	}, "fake-id-token")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"release-manager"}, u.Roles)
}
