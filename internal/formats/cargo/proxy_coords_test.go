package cargo_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/cargo"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// A proxied crate must be registered under its real name and version: the OSV
// scan queries by comp.Name/comp.Version, so a path-fallback name
// ("api/v1/crates/…/download") and placeholder version ("1") make every crate
// pulled through a Cargo proxy invisible to vulnerability scanning (#336).

func setupCargoProxy(t *testing.T, upstreamBody string) (*gin.Engine, *testutil.ComponentRepo) {
	t.Helper()
	orig := repoproxy.UpstreamClient
	repoproxy.UpstreamClient = &http.Client{}
	t.Cleanup(func() { repoproxy.UpstreamClient = orig })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte(upstreamBody))
	}))
	t.Cleanup(srv.Close)

	repo := &domain.Repository{
		ID: "repo-cargo-proxy", Name: "cargo-proxy", Format: domain.RepoFormat("cargo"),
		Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": srv.URL},
	}
	comps := testutil.NewComponentRepo()
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: comps,
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	h := cargo.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	return r, comps
}

func TestCargo_ProxyDownload_RegistersRealCrateCoords(t *testing.T) {
	r, comps := setupCargoProxy(t, "crate-bytes")

	req := httptest.NewRequest(http.MethodGet,
		"/repository/cargo-proxy/api/v1/crates/smallvec/1.6.1/download", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	page, err := comps.Search(context.Background(), domain.SearchParams{Repository: "cargo-proxy", Limit: 10})
	require.NoError(t, err)
	require.Len(t, page.Items, 1, "the cached crate must be registered as one component")
	assert.Equal(t, "smallvec", page.Items[0].Name,
		"the component carries the crate's real name — the OSV query is built from it")
	assert.Equal(t, "1.6.1", page.Items[0].Version,
		"the component carries the crate's real version — a placeholder never matches an advisory range")
}

// Index entries are versionless metadata; they follow the same convention the
// npm proxy uses (Version "metadata") instead of a path-mangled name.
func TestCargo_ProxyIndexEntry_RegistersMetadataCoords(t *testing.T) {
	r, comps := setupCargoProxy(t, `{"name":"smallvec","vers":"1.6.1"}`)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/cargo-proxy/index/sm/al/smallvec", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	page, err := comps.Search(context.Background(), domain.SearchParams{Repository: "cargo-proxy", Limit: 10})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assert.Equal(t, "smallvec", page.Items[0].Name,
		"the index entry is registered under the crate's name, not the sharded path")
	assert.Equal(t, "metadata", page.Items[0].Version)
}
