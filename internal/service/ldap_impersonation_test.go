package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/auth"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
)

// countingLDAP records whether the directory was asked at all.
type countingLDAP struct {
	mockLDAP
	calls int
}

func (m *countingLDAP) Authenticate(ctx context.Context, u, p string) (*auth.LDAPUser, error) {
	m.calls++
	return m.mockLDAP.Authenticate(ctx, u, p)
}

// GHSA-86xg-jx85-4hj7: "ADMIN" misses the case-sensitive lookup, the
// lowercased retry finds the local admin, and the directory entry uid=admin
// must not be allowed to log in as it — nor to rewrite its profile.
func TestLogin_LDAP_DifferentCaseDoesNotTakeOverNonLDAPAccount(t *testing.T) {
	for _, source := range []domain.UserSource{domain.UserSourceLocal, domain.UserSourceOIDC, domain.UserSourceSAML} {
		t.Run(string(source), func(t *testing.T) {
			ldap := &countingLDAP{mockLDAP: mockLDAP{user: &auth.LDAPUser{
				Username: "admin", Email: "attacker@evil.example", FirstName: "Mallory",
			}}}
			svc, users := newUserSvcWithLDAP(ldap)
			require.NoError(t, users.Create(context.Background(), &domain.User{
				Username: "admin", Email: "admin@corp.example", FirstName: "Admin",
				Status: domain.UserStatusActive, Source: source, Roles: []string{"nx-admin"},
			}))

			token, _, err := svc.Login(context.Background(), "ADMIN", "directory-password")
			require.Error(t, err)
			assert.True(t, errors.Is(err, service.ErrProvisioningConflict), "%v", err)
			assert.Empty(t, token)
			assert.Zero(t, ldap.calls, "the directory is not consulted for a non-LDAP account")

			u, _ := users.Get(context.Background(), "admin")
			require.NotNil(t, u)
			assert.Equal(t, "admin@corp.example", u.Email, "the profile is untouched")
			assert.Equal(t, "Admin", u.FirstName)
		})
	}
}

// A genuine LDAP account typed in another case still logs in.
func TestLogin_LDAP_DifferentCaseStillLogsInLDAPAccount(t *testing.T) {
	ldap := &countingLDAP{mockLDAP: mockLDAP{user: &auth.LDAPUser{Username: "svcdevops"}}}
	svc, users := newUserSvcWithLDAP(ldap)
	require.NoError(t, users.Create(context.Background(), &domain.User{
		Username: "svcdevops", Status: domain.UserStatusActive, Source: domain.UserSourceLDAP,
	}))
	token, u, err := svc.Login(context.Background(), "svcDevOps", "pw")
	require.NoError(t, err)
	assert.NotEmpty(t, token)
	assert.Equal(t, "svcdevops", u.Username)
}
