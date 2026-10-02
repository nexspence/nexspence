package cargo_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/cargo"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func authRequired(t *testing.T, rbac formats.RBACChecker, repo *domain.Repository) bool {
	t.Helper()
	repos := testutil.NewRepoRepo(repo)
	h := cargo.New(formats.Deps{
		Repos:      repos,
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
		RBAC:       rbac,
	})
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/repository/"+repo.Name+"/index/config.json", nil))
	require.Equal(t, http.StatusOK, w.Code)
	var cfg map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &cfg))
	v, ok := cfg["auth-required"].(bool)
	require.True(t, ok, "auth-required is a bool: %s", w.Body.String())
	return v
}

// #588: cargo sends its token on downloads only when config.json asks for
// it, so a repository that refuses anonymous reads must say so.
func TestCargo_IndexConfig_AuthRequiredFollowsAnonymousRead(t *testing.T) {
	private := testutil.SimpleRepo("priv", "cargo")
	private.AllowAnonymous = false
	public := testutil.SimpleRepo("pub", "cargo")
	public.AllowAnonymous = true

	rbac := service.NewRBACService(nil, testutil.NewRepoRepo(private, public), zap.NewNop().Sugar(), true)
	assert.True(t, authRequired(t, rbac, private))
	assert.False(t, authRequired(t, rbac, public))

	// Anonymous access switched off instance-wide: every repository is private.
	closed := service.NewRBACService(nil, testutil.NewRepoRepo(public), zap.NewNop().Sugar(), false)
	assert.True(t, authRequired(t, closed, public))
}

type failingRBAC struct{}

func (failingRBAC) CanAccessRepo(context.Context, string, []string, *domain.Repository, string, string) (bool, error) {
	return false, errors.New("db down")
}

// When the answer cannot be worked out, the token is asked for.
func TestCargo_IndexConfig_AuthRequiredOnRBACError(t *testing.T) {
	repo := testutil.SimpleRepo("err", "cargo")
	repo.AllowAnonymous = true
	assert.True(t, authRequired(t, failingRBAC{}, repo))
}
