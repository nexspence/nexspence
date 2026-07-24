package apt_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/apt"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// useUnguardedUpstream swaps the SSRF-guarded upstream client for a plain one so
// cache-miss fetches can reach the loopback httptest server used as a fake mirror.
func useUnguardedUpstream(t *testing.T) {
	t.Helper()
	orig := repoproxy.UpstreamClient
	repoproxy.UpstreamClient = &http.Client{}
	t.Cleanup(func() { repoproxy.UpstreamClient = orig })
}

// setupProxy wires an apt proxy repo pointed at upstream and returns the engine
// plus the component/asset mocks so tests can inspect what was cached.
func setupProxy(t *testing.T, name, upstream string) (*gin.Engine, *testutil.ComponentRepo, *testutil.AssetRepo) {
	t.Helper()
	repo := testutil.SimpleRepo(name, "apt")
	repo.Type = domain.TypeProxy
	repo.ProxyConfig = map[string]any{"remote_url": upstream}

	comps := testutil.NewComponentRepo()
	assets := testutil.NewAssetRepo()
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: comps,
		Assets:     assets,
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	h := apt.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	return r, comps, assets
}

func get(t *testing.T, r *gin.Engine, url string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil))
	return w
}

// A cached .deb must land on a component named after the package, with the
// version parsed out of the filename — same coordinates a hosted upload gets.
// Before this, apt's proxy branch passed empty coords and every cached file
// shared one nameless component (#76).
func TestAptProxy_CachedDeb_HasPackageCoordinates(t *testing.T) {
	useUnguardedUpstream(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("deb-bytes"))
	}))
	defer upstream.Close()

	r, comps, _ := setupProxy(t, "apt-proxy", upstream.URL)

	w := get(t, r, "/repository/apt-proxy/pool/main/n/nginx/nginx_1.24.0-1_amd64.deb")
	require.Equal(t, http.StatusOK, w.Code)

	page, err := comps.List(t.Context(), "apt-proxy", 100, 0)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assert.Equal(t, "nginx", page.Items[0].Name)
	assert.Equal(t, "1.24.0-1", page.Items[0].Version)
}

// Metadata files are not packages: they get their own component keyed by path,
// so they stay individually browsable and deletable.
func TestAptProxy_CachedMetadata_IsPerPathComponent(t *testing.T) {
	useUnguardedUpstream(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("Suite: trixie\n"))
	}))
	defer upstream.Close()

	r, comps, _ := setupProxy(t, "apt-proxy", upstream.URL)

	require.Equal(t, http.StatusOK, get(t, r, "/repository/apt-proxy/dists/trixie/InRelease").Code)
	require.Equal(t, http.StatusOK, get(t, r, "/repository/apt-proxy/dists/trixie/Release").Code)

	page, err := comps.List(t.Context(), "apt-proxy", 100, 0)
	require.NoError(t, err)
	require.Len(t, page.Items, 2, "each metadata file is its own component")
	names := map[string]bool{}
	for _, c := range page.Items {
		names[c.Name] = true
		assert.NotEmpty(t, c.Name, "component name must never be empty")
	}
	assert.True(t, names["dists/trixie/InRelease"])
	assert.True(t, names["dists/trixie/Release"])
}

// Every cached asset must be reachable from the component that owns it —
// this is what the browse row needs in order to delete it.
func TestAptProxy_CachedAsset_LinkedToComponent(t *testing.T) {
	useUnguardedUpstream(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("deb-bytes"))
	}))
	defer upstream.Close()

	r, comps, assets := setupProxy(t, "apt-proxy", upstream.URL)
	require.Equal(t, http.StatusOK,
		get(t, r, "/repository/apt-proxy/pool/main/v/vim/vim_9.1_amd64.deb").Code)

	page, err := comps.List(t.Context(), "apt-proxy", 100, 0)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)

	owned, err := assets.ListByComponentID(t.Context(), page.Items[0].ID)
	require.NoError(t, err)
	require.Len(t, owned, 1)
	assert.Equal(t, "/pool/main/v/vim/vim_9.1_amd64.deb", owned[0].Path)
}
