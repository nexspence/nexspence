package cargo_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
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

// ── #347: sparse index against a REAL registry's URL shape ───────────────────

// realShapeIndexUpstream mimics index.crates.io: sparse-index keys live at the
// ROOT ("/se/rd/serde"), never under "/index/..." (that prefix is this
// codebase's own route), and /config.json advertises where downloads live —
// possibly on a DIFFERENT host, exactly like the real crates.io split.
func realShapeCargoUpstreams(t *testing.T) (indexHost *httptest.Server, dlHits *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	dlHost := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/crates/serde/1.0.0/download" {
			hits.Add(1)
			_, _ = w.Write([]byte("crate-bytes"))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(dlHost.Close)

	indexHost = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/config.json":
			_, _ = w.Write([]byte(`{"dl":"` + dlHost.URL + `/api/v1/crates","api":"` + dlHost.URL + `"}`))
		case "/se/rd/serde":
			_, _ = w.Write([]byte(`{"name":"serde","vers":"1.0.0","deps":[],"cksum":"aa"}` + "\n"))
		default:
			// The real index host is an S3 bucket: anything else — including
			// the doubled "/index/..." path — is NoSuchKey.
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(indexHost.Close)
	return indexHost, &hits
}

func setupCargoProxyAt(t *testing.T, upstreamURL string) (*gin.Engine, formats.Deps) {
	t.Helper()
	orig := repoproxy.UpstreamClient
	repoproxy.UpstreamClient = &http.Client{}
	t.Cleanup(func() { repoproxy.UpstreamClient = orig })

	repo := &domain.Repository{
		ID: "repo-cargo-real", Name: "cargo-real", Format: domain.RepoFormat("cargo"),
		Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": upstreamURL},
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
	return r, d
}

func TestCargo_ProxyIndex_StripsLocalPrefixForUpstream(t *testing.T) {
	indexHost, _ := realShapeCargoUpstreams(t)
	r, _ := setupCargoProxyAt(t, indexHost.URL)

	req := httptest.NewRequest(http.MethodGet, "/repository/cargo-real/index/se/rd/serde", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code,
		"the /index/ route prefix must be stripped before the request leaves for upstream: %d %s", w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"serde"`)
}

// The real crates.io serves downloads on a different host than its sparse
// index; the dl base comes from the upstream's own /config.json.
func TestCargo_ProxyDownload_FollowsUpstreamConfigDl(t *testing.T) {
	indexHost, dlHits := realShapeCargoUpstreams(t)
	r, d := setupCargoProxyAt(t, indexHost.URL)

	req := httptest.NewRequest(http.MethodGet, "/repository/cargo-real/api/v1/crates/serde/1.0.0/download", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code,
		"the download must follow the upstream config.json dl host: %d %s", w.Code, w.Body.String())
	assert.Equal(t, "crate-bytes", w.Body.String())
	require.Equal(t, int32(1), dlHits.Load())

	// The crate is cached: a second pull is served locally (immutable).
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, httptest.NewRequest(http.MethodGet, "/repository/cargo-real/api/v1/crates/serde/1.0.0/download", nil))
	require.Equal(t, http.StatusOK, w2.Code)
	assert.Equal(t, "crate-bytes", w2.Body.String())
	assert.Equal(t, int32(1), dlHits.Load(), "an immutable crate must be served from cache")

	comp, err := findCargoComponent(d, "cargo-real", "serde")
	require.NoError(t, err)
	assert.Equal(t, "1.0.0", comp.Version)
}

func findCargoComponent(d formats.Deps, repo, name string) (*domain.Component, error) {
	page, err := d.Components.Search(nil, domain.SearchParams{Repository: repo, Name: name, Limit: 10}) //nolint:staticcheck // mock ignores ctx
	if err != nil {
		return nil, err
	}
	for i := range page.Items {
		if page.Items[i].Name == name {
			return &page.Items[i], nil
		}
	}
	return nil, fmt.Errorf("component %q not found", name)
}
