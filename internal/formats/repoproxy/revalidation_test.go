package repoproxy_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// fakeUpstream is a controllable upstream mirror for revalidation tests.
type fakeUpstream struct {
	mu sync.Mutex
	// body is the current representation returned for a 200.
	body string
	// return304 makes the server answer 304 when the request carries a
	// conditional header (If-Modified-Since / If-None-Match).
	return304 bool
	// requests records the total number of requests received.
	requests int
	// lastIfModifiedSince captures the conditional header seen on the last request.
	lastIfModifiedSince string
}

func newFakeUpstream(body string) (*fakeUpstream, *httptest.Server) {
	fu := &fakeUpstream{body: body}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fu.mu.Lock()
		fu.requests++
		fu.lastIfModifiedSince = r.Header.Get("If-Modified-Since")
		conditional := fu.lastIfModifiedSince != "" || r.Header.Get("If-None-Match") != ""
		want304 := fu.return304
		body := fu.body
		fu.mu.Unlock()

		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
		if want304 && conditional {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}))
	return fu, srv
}

func (fu *fakeUpstream) set(body string, return304 bool) {
	fu.mu.Lock()
	defer fu.mu.Unlock()
	fu.body = body
	fu.return304 = return304
}

func (fu *fakeUpstream) count() int {
	fu.mu.Lock()
	defer fu.mu.Unlock()
	return fu.requests
}

func newRevalDeps(repo *domain.Repository) (formats.Deps, *testutil.AssetRepo, *testutil.BlobStore) {
	assets := testutil.NewAssetRepo()
	blobStore := testutil.NewBlobStore()
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     assets,
		BlobStore:  blobStore,
	}
	return d, assets, blobStore
}

func revalGET(d formats.Deps, repo *domain.Repository, path string, maxAge time.Duration) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, path, nil)
	_ = repoproxy.ServeGET(c, d, repo, path, "", base.Coords{Name: "meta", Version: "metadata"}, "text/plain", maxAge)
	return w
}

// makeStale rewinds the cached asset's freshness timestamp so it is older than any TTL.
func makeStale(t *testing.T, assets *testutil.AssetRepo, repoName, path string) {
	t.Helper()
	a, err := assets.GetByPath(nil, repoName, path) //nolint:staticcheck // mock ignores ctx
	require.NoError(t, err)
	a.LastModified = time.Now().Add(-1 * time.Hour)
}

// (a) Immutable content (maxAge == 0) is served from cache without contacting upstream.
func TestServeGET_Immutable_NoRevalidation(t *testing.T) {
	useUnguardedUpstream(t)
	fu, srv := newFakeUpstream("artifact-v1")
	defer srv.Close()

	repo := proxyRepo("immproxy", srv.URL)
	d, assets, _ := newRevalDeps(repo)

	// First GET: cache miss → fetch upstream.
	w1 := revalGET(d, repo, "/pkg/1.0/a.jar", 0)
	assert.Equal(t, http.StatusOK, w1.Code)
	assert.Equal(t, "artifact-v1", w1.Body.String())
	require.Equal(t, 1, fu.count())

	// Even after the asset is ancient, maxAge==0 must never revalidate.
	makeStale(t, assets, "immproxy", "/pkg/1.0/a.jar")
	w2 := revalGET(d, repo, "/pkg/1.0/a.jar", 0)
	assert.Equal(t, http.StatusOK, w2.Code)
	assert.Equal(t, "artifact-v1", w2.Body.String())
	assert.Equal(t, 1, fu.count(), "immutable cache hit must not contact upstream")
}

// (b) Metadata within its TTL is served from cache without revalidation.
func TestServeGET_Metadata_WithinTTL_NoRevalidation(t *testing.T) {
	useUnguardedUpstream(t)
	fu, srv := newFakeUpstream("InRelease-v1")
	defer srv.Close()

	repo := proxyRepo("freshproxy", srv.URL)
	d, _, _ := newRevalDeps(repo)

	w1 := revalGET(d, repo, "/dists/trixie/InRelease", 10*time.Minute)
	require.Equal(t, http.StatusOK, w1.Code)
	require.Equal(t, 1, fu.count())

	// Second GET immediately: asset is fresh (< TTL) → cache hit, no upstream call.
	w2 := revalGET(d, repo, "/dists/trixie/InRelease", 10*time.Minute)
	assert.Equal(t, http.StatusOK, w2.Code)
	assert.Equal(t, "InRelease-v1", w2.Body.String())
	assert.Equal(t, 1, fu.count(), "fresh metadata must not revalidate")
}

// (c) Metadata past its TTL triggers a conditional GET; a 304 refreshes freshness and serves cache.
func TestServeGET_Metadata_PastTTL_304_RefreshesAndServesCache(t *testing.T) {
	useUnguardedUpstream(t)
	fu, srv := newFakeUpstream("InRelease-v1")
	defer srv.Close()

	repo := proxyRepo("staleproxy", srv.URL)
	d, assets, _ := newRevalDeps(repo)

	// Prime the cache.
	require.Equal(t, http.StatusOK, revalGET(d, repo, "/dists/trixie/InRelease", 10*time.Minute).Code)
	require.Equal(t, 1, fu.count())

	// Make it stale and have upstream answer 304 to conditional requests.
	makeStale(t, assets, "staleproxy", "/dists/trixie/InRelease")
	fu.set("InRelease-v1", true)

	w := revalGET(d, repo, "/dists/trixie/InRelease", 10*time.Minute)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "InRelease-v1", w.Body.String(), "cache served after 304")
	assert.Equal(t, 2, fu.count(), "stale metadata must revalidate against upstream")

	fu.mu.Lock()
	ims := fu.lastIfModifiedSince
	fu.mu.Unlock()
	assert.NotEmpty(t, ims, "revalidation must send a conditional If-Modified-Since header")

	// Freshness refreshed: the asset is no longer stale, so the next GET serves cache silently.
	a, err := assets.GetByPath(nil, "staleproxy", "/dists/trixie/InRelease") //nolint:staticcheck
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now(), a.LastModified, 5*time.Second, "304 must refresh last_modified")

	require.Equal(t, http.StatusOK, revalGET(d, repo, "/dists/trixie/InRelease", 10*time.Minute).Code)
	assert.Equal(t, 2, fu.count(), "refreshed metadata is within TTL again → no second revalidation")
}

// (d) Metadata past its TTL where upstream returns 200 replaces the cached copy.
func TestServeGET_Metadata_PastTTL_200_ReplacesCache(t *testing.T) {
	useUnguardedUpstream(t)
	fu, srv := newFakeUpstream("InRelease-v1")
	defer srv.Close()

	repo := proxyRepo("updateproxy", srv.URL)
	d, assets, blobStore := newRevalDeps(repo)

	require.Equal(t, http.StatusOK, revalGET(d, repo, "/dists/trixie/InRelease", 10*time.Minute).Code)
	require.Equal(t, "InRelease-v1", func() string { return blobStoreBody(t, blobStore, "updateproxy", "/dists/trixie/InRelease") }())

	// Stale + upstream now serves a new representation (200, not 304).
	makeStale(t, assets, "updateproxy", "/dists/trixie/InRelease")
	fu.set("InRelease-v2", false)

	w := revalGET(d, repo, "/dists/trixie/InRelease", 10*time.Minute)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "InRelease-v2", w.Body.String(), "updated upstream body served to client")
	assert.Equal(t, 2, fu.count())

	// Cache blob replaced with v2.
	assert.Equal(t, "InRelease-v2", blobStoreBody(t, blobStore, "updateproxy", "/dists/trixie/InRelease"))

	// A subsequent within-TTL GET serves the new copy from cache.
	w2 := revalGET(d, repo, "/dists/trixie/InRelease", 10*time.Minute)
	assert.Equal(t, "InRelease-v2", w2.Body.String())
	assert.Equal(t, 2, fu.count())
}

// (e) When upstream is down past the TTL, the stale cache is served rather than erroring.
func TestServeGET_Metadata_PastTTL_UpstreamDown_ServesStale(t *testing.T) {
	useUnguardedUpstream(t)
	fu, srv := newFakeUpstream("InRelease-v1")

	repo := proxyRepo("downproxy", srv.URL)
	d, assets, _ := newRevalDeps(repo)

	require.Equal(t, http.StatusOK, revalGET(d, repo, "/dists/trixie/InRelease", 10*time.Minute).Code)
	require.Equal(t, 1, fu.count())

	// Take upstream offline and force staleness.
	srv.Close()
	makeStale(t, assets, "downproxy", "/dists/trixie/InRelease")

	w := revalGET(d, repo, "/dists/trixie/InRelease", 10*time.Minute)
	assert.Equal(t, http.StatusOK, w.Code, "upstream outage must not break metadata reads")
	assert.Equal(t, "InRelease-v1", w.Body.String(), "stale cache served on upstream failure")
}

// blobStoreBody reads the cached blob for a repo path and returns it as a string.
func blobStoreBody(t *testing.T, bs *testutil.BlobStore, repoName, path string) string {
	t.Helper()
	rc, _, err := bs.Get(nil, base.BlobKey(repoName, path)) //nolint:staticcheck // mock ignores ctx
	require.NoError(t, err)
	defer func() { _ = rc.Close() }()
	buf, err := io.ReadAll(rc)
	require.NoError(t, err)
	return string(buf)
}
