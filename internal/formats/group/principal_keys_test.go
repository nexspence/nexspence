package group

// The auth layer is the only place that decides who the caller is, so the
// group's copy of the principal must not drift from the set of keys that layer
// writes. These tests read that set from the auth layer's own source instead
// of from a list kept next to it: a key added to the auth layer that the group
// does not copy would be dropped silently on the way to the member, and the
// member would apply its checks against a partial principal.
//
// The auth layer lives in internal/api/handlers and writes every context key
// as c.Set("key", …) — that is where the request principal is established
// (AuthMiddleware, authenticateBearer, setAPITokenAuth, setTokenScopes).

import (
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// nonPrincipalAuthKeys are the keys the auth layer sets that are not part of
// the caller's identity, so the group does not have to copy them.
//
// "audit_source" records which login channel established the session, for the
// audit trail. The SSO handlers set it on the callback response only — it
// never rides a repository request — and no member handler reads it.
var nonPrincipalAuthKeys = map[string]bool{"audit_source": true}

// TestPrincipalKeys_CoverEveryKeyTheAuthLayerSets pins the group's copy list
// against the auth layer's own: every key the auth layer sets that is part of
// the identity must be copied to the member, and the group must not copy a
// key the auth layer never sets.
//
// A new key in the auth layer fails this test until the group copies it too —
// that is the whole point: the copy list cannot drift silently.
func TestPrincipalKeys_CoverEveryKeyTheAuthLayerSets(t *testing.T) {
	authKeys := authContextKeys(t)

	// A reader that silently found nothing would make every assertion below
	// vacuous, so the two keys every authenticated request carries are the
	// floor the reader has to clear.
	require.Contains(t, authKeys, "username")
	require.Contains(t, authKeys, "roles")

	var missing []string
	for _, key := range authKeys {
		if nonPrincipalAuthKeys[key] {
			continue
		}
		if !slices.Contains(principalKeys, key) {
			missing = append(missing, key)
		}
	}
	assert.Empty(t, missing,
		"the auth layer sets %v, which the group does not copy to its members — a member would apply "+
			"its checks against a partial principal", missing)

	var unknown []string
	for _, key := range principalKeys {
		if !slices.Contains(authKeys, key) {
			unknown = append(unknown, key)
		}
	}
	assert.Empty(t, unknown,
		"the group copies %v, which the auth layer never sets", unknown)
}

// TestMemberSeesEveryKeyTheAuthLayerSets proves the copy end to end: for
// every key the auth layer sets, a member reached through a group sees it.
// It fails if the group stops copying a key — the token-binding trio
// (tokenScopes, apiTokenID, apiTokenExpiresAt) among them, whose loss would
// let a member mint an unbound, unscoped session for a caller who logged in
// with a scoped API token (#566/#567).
func TestMemberSeesEveryKeyTheAuthLayerSets(t *testing.T) {
	spy := &spyFmt{}
	principal := map[string]any{}
	for _, key := range authContextKeys(t) {
		if nonPrincipalAuthKeys[key] {
			continue
		}
		principal[key] = "value-" + key
	}
	r := spyEngine(t, spy, principal)

	req := httptest.NewRequest(http.MethodGet, "/repository/spy-grp/file.txt", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	for _, key := range slices.Sorted(maps.Keys(principal)) {
		got, ok := spy.keys[key]
		require.True(t, ok, "the member never saw %q", key)
		assert.Equal(t, principal[key], got, "the member saw a different %q", key)
	}
}

// spyFmt is a member handler that records the context keys it was called
// with — the point of the group layer is the context the member sees.
type spyFmt struct {
	keys map[any]any
}

func (s *spyFmt) Name() string { return "spyfmt" }

func (s *spyFmt) ServeHTTP(c *gin.Context) {
	s.keys = maps.Clone(c.Keys)
	c.String(http.StatusOK, "served")
}

// spyEngine builds a group engine whose member is a spy and whose request
// carries the given principal, the way the auth middleware would leave it.
func spyEngine(t *testing.T, spy *spyFmt, principal map[string]any) *gin.Engine {
	t.Helper()

	member := testutil.SimpleRepo("spy-member", "spyfmt")
	grp := &domain.Repository{
		ID: "repo-spy-grp", Name: "spy-grp", Format: "spyfmt",
		Type: domain.TypeGroup, Online: true,
		FormatConfig: map[string]any{"member_names": []interface{}{"spy-member"}},
	}
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(member, grp),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	groupH := New(d, map[string]formats.FormatHandler{"spyfmt": spy})

	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) {
		for k, v := range principal {
			c.Set(k, v)
		}
		groupH.ServeHTTP(c)
	})
	return r
}

// authContextKeys returns the gin context keys the auth layer sets, read from
// the auth layer's own source.
func authContextKeys(t *testing.T) []string {
	t.Helper()

	dir := filepath.Join(repoRoot(t), "internal", "api", "handlers")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	keys := map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		require.NoError(t, err)

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Set" || len(call.Args) == 0 {
				return true
			}
			ident, ok := sel.X.(*ast.Ident)
			if !ok {
				// A header write (w.Header().Set(…)), not a context key.
				return true
			}
			if ident.Name == "params" {
				// url.Values, not the gin context.
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			keys[strings.Trim(lit.Value, `"`)] = true
			return true
		})
	}

	out := slices.Sorted(maps.Keys(keys))
	require.NotEmpty(t, out, "no context keys found in the auth layer — the reader is out of date")
	return out
}

// repoRoot returns the repository root, derived from this file's own path so
// the test runs wherever the package is built.
func repoRoot(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok, "cannot locate the test file")
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}
