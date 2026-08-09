package repoproxy_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// Quota enforcement on the proxy cache-fill path (#189): when caching an
// upstream artifact would exceed the repository or blob-store quota, the
// artifact is streamed to the client but NOT cached — the client keeps
// working, the cache stops growing.

const overQuotaBody = "twenty-bytes-payload" // 20 bytes

// quotaDeps builds proxy deps around repo with a fresh asset repo and blob store.
func quotaDeps(repo *domain.Repository, stores ...*domain.BlobStore) (formats.Deps, *testutil.AssetRepo, *testutil.BlobStore) {
	assets := testutil.NewAssetRepo()
	blobStore := testutil.NewBlobStore()
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      testutil.NewBlobStoreRepo(stores...),
		Components: testutil.NewComponentRepo(),
		Assets:     assets,
		BlobStore:  blobStore,
	}
	return d, assets, blobStore
}

func quotaGET(d formats.Deps, repo *domain.Repository, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, path, nil)
	_ = repoproxy.ServeGET(c, d, repo, path, "", base.Coords{Name: "pkg", Version: "1.0"}, "application/octet-stream", 0)
	return w
}

// requireNotCached asserts that neither a DB asset nor a blob exists for path.
func requireNotCached(t *testing.T, assets *testutil.AssetRepo, blobStore *testutil.BlobStore, repoName, path string) {
	t.Helper()
	_, err := assets.GetByPath(nil, repoName, path) //nolint:staticcheck // mock ignores ctx
	assert.True(t, errors.Is(err, repository.ErrNotFound), "asset must not be registered, got err=%v", err)
	exists, err := blobStore.Exists(nil, base.BlobKey(repoName, path)) //nolint:staticcheck // mock ignores ctx
	require.NoError(t, err)
	assert.False(t, exists, "blob must not be cached")
}

// Repository quota: a cache fill whose declared size exceeds repo.QuotaBytes
// serves the client from upstream without caching.
func TestServeGET_CacheFill_RepoQuotaExceeded_ServesWithoutCaching(t *testing.T) {
	useUnguardedUpstream(t)
	fu, srv := newFakeUpstream(overQuotaBody)
	defer srv.Close()

	repo := proxyRepo("quotaproxy", srv.URL)
	quota := int64(10)
	repo.QuotaBytes = &quota
	d, assets, blobStore := quotaDeps(repo)

	w := quotaGET(d, repo, "/pkg/1.0/a.jar")
	assert.Equal(t, http.StatusOK, w.Code, "client must still get the artifact")
	assert.Equal(t, overQuotaBody, w.Body.String())
	requireNotCached(t, assets, blobStore, "quotaproxy", "/pkg/1.0/a.jar")

	// Nothing was cached, so a second GET must go upstream again.
	w2 := quotaGET(d, repo, "/pkg/1.0/a.jar")
	assert.Equal(t, http.StatusOK, w2.Code)
	assert.Equal(t, overQuotaBody, w2.Body.String())
	assert.Equal(t, 2, fu.count(), "uncached artifact must be re-fetched upstream")
}

// Blob-store quota: the same skip-cache behavior when the repo's blob store
// (here the default) is at capacity.
func TestServeGET_CacheFill_BlobStoreQuotaExceeded_ServesWithoutCaching(t *testing.T) {
	useUnguardedUpstream(t)
	_, srv := newFakeUpstream(overQuotaBody)
	defer srv.Close()

	quota := int64(10)
	repo := proxyRepo("bsquotaproxy", srv.URL)
	d, assets, blobStore := quotaDeps(repo, &domain.BlobStore{
		ID:         "00000000-0000-0000-0000-000000000001",
		Name:       "default",
		Type:       "local",
		QuotaBytes: &quota,
		UsedBytes:  5,
	})

	w := quotaGET(d, repo, "/pkg/1.0/a.jar")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, overQuotaBody, w.Body.String())
	requireNotCached(t, assets, blobStore, "bsquotaproxy", "/pkg/1.0/a.jar")
}

// Unknown Content-Length: the quota can only be checked after streaming; an
// over-quota blob must then be dropped from the cache and left unregistered.
func TestServeGET_CacheFill_UnknownLength_OverQuota_DropsCacheAfterStream(t *testing.T) {
	useUnguardedUpstream(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		// Flush between writes so the response goes out chunked, without Content-Length.
		fl := w.(http.Flusher)
		_, _ = w.Write([]byte(overQuotaBody[:10]))
		fl.Flush()
		_, _ = w.Write([]byte(overQuotaBody[10:]))
	}))
	defer srv.Close()

	repo := proxyRepo("chunkquota", srv.URL)
	quota := int64(10)
	repo.QuotaBytes = &quota
	d, assets, blobStore := quotaDeps(repo)

	w := quotaGET(d, repo, "/pkg/1.0/a.jar")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, overQuotaBody, w.Body.String(), "client already received the stream")
	requireNotCached(t, assets, blobStore, "chunkquota", "/pkg/1.0/a.jar")
}

// Rewritten metadata (buffered path): over-quota bodies are served rewritten
// but not cached.
func TestServeGETRewritten_CacheFill_QuotaExceeded_ServesWithoutCaching(t *testing.T) {
	useUnguardedUpstream(t)
	_, srv := newFakeUpstream(overQuotaBody)
	defer srv.Close()

	repo := proxyRepo("rwquota", srv.URL)
	quota := int64(10)
	repo.QuotaBytes = &quota
	d, assets, blobStore := quotaDeps(repo)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/meta.json", nil)
	rewrite := func(b []byte) []byte { return append([]byte("rw:"), b...) }
	err := repoproxy.ServeGETRewritten(c, d, repo, "/meta.json", "", base.Coords{Name: "meta"}, "text/plain", 0, rewrite)
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "rw:"+overQuotaBody, w.Body.String(), "rewritten body still served")
	requireNotCached(t, assets, blobStore, "rwquota", "/meta.json")
}

// Control: a fill within quota must still be cached — enforcement must not
// block legitimate cache fills.
func TestServeGET_CacheFill_WithinQuota_StillCaches(t *testing.T) {
	useUnguardedUpstream(t)
	fu, srv := newFakeUpstream(overQuotaBody)
	defer srv.Close()

	repo := proxyRepo("underquota", srv.URL)
	quota := int64(100)
	repo.QuotaBytes = &quota
	d, assets, blobStore := quotaDeps(repo)

	w := quotaGET(d, repo, "/pkg/1.0/a.jar")
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, overQuotaBody, w.Body.String())

	a, err := assets.GetByPath(nil, "underquota", "/pkg/1.0/a.jar") //nolint:staticcheck // mock ignores ctx
	require.NoError(t, err)
	assert.Equal(t, int64(len(overQuotaBody)), a.SizeBytes)
	assert.Equal(t, overQuotaBody, blobStoreBody(t, blobStore, "underquota", "/pkg/1.0/a.jar"))

	// Cached → second GET does not contact upstream.
	w2 := quotaGET(d, repo, "/pkg/1.0/a.jar")
	assert.Equal(t, overQuotaBody, w2.Body.String())
	assert.Equal(t, 1, fu.count())
}
