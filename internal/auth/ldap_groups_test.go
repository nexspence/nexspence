package auth

import (
	"errors"
	"testing"

	"github.com/go-ldap/ldap/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/config"
)

// fakeLDAPConn stands in for *ldap.Conn during the group-search step.
type fakeLDAPConn struct {
	searchRes *ldap.SearchResult
	searchErr error
	bindErr   error
	searched  bool
}

func (c *fakeLDAPConn) Bind(_, _ string) error             { return c.bindErr }
func (c *fakeLDAPConn) UnauthenticatedBind(_ string) error { return c.bindErr }
func (c *fakeLDAPConn) Search(_ *ldap.SearchRequest) (*ldap.SearchResult, error) {
	c.searched = true
	return c.searchRes, c.searchErr
}

func groupCfg() config.LDAPConfig {
	return config.LDAPConfig{
		GroupBase:      "ou=groups,dc=example,dc=com",
		GroupFilter:    "(member={dn})",
		GroupAttribute: "cn",
	}
}

func TestFetchGroups_NotConfigured_DoesNotSearch(t *testing.T) {
	svc := &LDAPService{cfg: config.LDAPConfig{}}
	conn := &fakeLDAPConn{}
	lu := &LDAPUser{DN: "uid=alice,dc=example,dc=com"}

	svc.fetchGroups(conn, lu)

	assert.False(t, conn.searched)
	assert.False(t, lu.GroupsSearched)
	assert.Empty(t, lu.GroupSearchErr)
	assert.Nil(t, lu.Groups)
}

func TestFetchGroups_SearchFails_ReportsErrorNotSearched(t *testing.T) {
	svc := &LDAPService{cfg: groupCfg()}
	conn := &fakeLDAPConn{searchErr: errors.New("LDAP Result Code 51 \"Busy\"")}
	lu := &LDAPUser{DN: "uid=alice,dc=example,dc=com"}

	svc.fetchGroups(conn, lu)

	assert.True(t, conn.searched)
	assert.False(t, lu.GroupsSearched, "a failed search is not evidence of zero groups")
	assert.Contains(t, lu.GroupSearchErr, "Busy")
	assert.Nil(t, lu.Groups)
}

func TestFetchGroups_SearchSucceeds_MarksSearched(t *testing.T) {
	svc := &LDAPService{cfg: groupCfg()}
	conn := &fakeLDAPConn{searchRes: &ldap.SearchResult{Entries: []*ldap.Entry{
		ldap.NewEntry("cn=developers,ou=groups,dc=example,dc=com", map[string][]string{"cn": {"developers"}}),
		ldap.NewEntry("cn=ops,ou=groups,dc=example,dc=com", map[string][]string{"cn": {"ops"}}),
	}}}
	lu := &LDAPUser{DN: "uid=alice,dc=example,dc=com"}

	svc.fetchGroups(conn, lu)

	assert.True(t, lu.GroupsSearched)
	assert.Empty(t, lu.GroupSearchErr)
	assert.Equal(t, []string{"developers", "ops"}, lu.Groups)
}

func TestFetchGroups_SearchSucceedsEmpty_MarksSearched(t *testing.T) {
	svc := &LDAPService{cfg: groupCfg()}
	conn := &fakeLDAPConn{searchRes: &ldap.SearchResult{}}
	lu := &LDAPUser{DN: "uid=alice,dc=example,dc=com"}

	svc.fetchGroups(conn, lu)

	require.True(t, lu.GroupsSearched, "an empty result is a confirmed answer")
	assert.Empty(t, lu.Groups)
}

func TestFetchGroups_ServiceRebindFails_SearchAsUserStillCounts(t *testing.T) {
	// serviceBind failure falls through to searching under the user's bind;
	// if that search succeeds the result is authoritative.
	cfg := groupCfg()
	cfg.BindDN = "cn=svc,dc=example,dc=com"
	svc := &LDAPService{cfg: cfg}
	conn := &fakeLDAPConn{bindErr: errors.New("invalid credentials"), searchRes: &ldap.SearchResult{}}
	lu := &LDAPUser{DN: "uid=alice,dc=example,dc=com"}

	svc.fetchGroups(conn, lu)

	assert.True(t, lu.GroupsSearched)
	assert.Empty(t, lu.GroupSearchErr)
}
