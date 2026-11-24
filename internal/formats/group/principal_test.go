package group_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/auth"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/group"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// seenPrincipal is what a member handler saw in its own context.
type seenPrincipal struct {
	repoName     string
	hasUsername  bool
	username     string
	hasUserID    bool
	userID       string
	hasRoles     bool
	roles        []string
	hasClaims    bool
	claims       *auth.Claims
	hasGroupRepo bool
}

// principalFmt is a fake member format handler that records the principal it
// was called with. The point of the group layer is the context the member
// sees, so the member reads it out of its context — as Conan's credential
// handshake does — rather than answering from the request.
type principalFmt struct {
	seen *seenPrincipal
}

func (p *principalFmt) Name() string { return "principalfmt" }

func (p *principalFmt) ServeHTTP(c *gin.Context) {
	p.seen.repoName = c.Param("repoName")
	p.seen.hasGroupRepo = c.GetBool(formats.GroupMemberKey)
	if v, ok := c.Get("username"); ok {
		p.seen.hasUsername = true
		p.seen.username, _ = v.(string)
	}
	if v, ok := c.Get("userID"); ok {
		p.seen.hasUserID = true
		p.seen.userID, _ = v.(string)
	}
	if v, ok := c.Get("roles"); ok {
		p.seen.hasRoles = true
		p.seen.roles, _ = v.([]string)
	}
	if v, ok := c.Get("claims"); ok {
		p.seen.hasClaims = true
		p.seen.claims, _ = v.(*auth.Claims)
	}
	c.String(http.StatusOK, "served")
}

// principalEngine builds a group engine whose request carries the given
// principal — as the auth middleware would leave it on the context — and a
// member that records what it saw.
func principalEngine(t *testing.T, principal map[string]any, seen *seenPrincipal) *gin.Engine {
	t.Helper()

	member := testutil.SimpleRepo("principal-member", "principalfmt")
	grp := &domain.Repository{
		ID: "repo-principal-grp", Name: "principal-grp", Format: "principalfmt",
		Type: domain.TypeGroup, Online: true,
		FormatConfig: map[string]any{"member_names": []interface{}{"principal-member"}},
	}

	repoRepo := testutil.NewRepoRepo(member, grp)
	d := formats.Deps{
		Repos:      repoRepo,
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	groupH := group.New(d, map[string]formats.FormatHandler{"principalfmt": &principalFmt{seen: seen}})

	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) {
		for k, v := range principal {
			c.Set(k, v)
		}
		groupH.ServeHTTP(c)
	})
	return r
}

// TestGroup_MemberSeesCallersPrincipal: a member reached through a group sees
// the caller the client presented to the group — the same username, user id,
// roles and claims — so the member's own checks run against the real user and
// the member's own repository name.
func TestGroup_MemberSeesCallersPrincipal(t *testing.T) {
	claims := &auth.Claims{UserID: "u-1", Username: "alice", Roles: []string{"nx-admin"}}
	seen := &seenPrincipal{}
	r := principalEngine(t, map[string]any{
		"username": "alice",
		"userID":   "u-1",
		"roles":    []string{"nx-admin"},
		"claims":   claims,
	}, seen)

	req := httptest.NewRequest(http.MethodGet, "/repository/principal-grp/file.txt", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	assert.True(t, seen.hasUsername, "member saw no username")
	assert.Equal(t, "alice", seen.username)
	assert.True(t, seen.hasUserID, "member saw no user id")
	assert.Equal(t, "u-1", seen.userID)
	assert.True(t, seen.hasRoles, "member saw no roles")
	assert.Equal(t, []string{"nx-admin"}, seen.roles)
	assert.True(t, seen.hasClaims, "member saw no claims")
	assert.Same(t, claims, seen.claims)
	// The group is a facade: the member answers for itself, under its own name.
	assert.Equal(t, "principal-member", seen.repoName)
}

// TestGroup_AnonymousRequest_StaysAnonymousForMember: a request that reached
// the group without a principal must not gain one on the way in.
func TestGroup_AnonymousRequest_StaysAnonymousForMember(t *testing.T) {
	seen := &seenPrincipal{}
	r := principalEngine(t, nil, seen)

	req := httptest.NewRequest(http.MethodGet, "/repository/principal-grp/file.txt", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	assert.False(t, seen.hasUsername, "an anonymous caller gained a username")
	assert.False(t, seen.hasUserID, "an anonymous caller gained a user id")
	assert.False(t, seen.hasRoles, "an anonymous caller gained roles")
	assert.False(t, seen.hasClaims, "an anonymous caller gained claims")
}

// TestGroup_WriteToMember_KeepsCallersPrincipal: the write path delegates to
// a member too, and the member must see the same caller there.
func TestGroup_WriteToMember_KeepsCallersPrincipal(t *testing.T) {
	seen := &seenPrincipal{}
	r := principalEngine(t, map[string]any{
		"username": "alice",
		"userID":   "u-1",
		"roles":    []string{"nx-admin"},
	}, seen)

	req := httptest.NewRequest(http.MethodPut, "/repository/principal-grp/file.txt", strings.NewReader("body"))
	req.ContentLength = 4
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	assert.True(t, seen.hasUsername, "member saw no username")
	assert.Equal(t, "alice", seen.username)
	assert.Equal(t, "u-1", seen.userID)
	assert.Equal(t, []string{"nx-admin"}, seen.roles)
	assert.Equal(t, "principal-member", seen.repoName)
}
