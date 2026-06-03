package repoproxy_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func init() { gin.SetMode(gin.TestMode) }

func proxyRepo(name, remoteURL string) *domain.Repository {
	return &domain.Repository{
		ID:          "repo-" + name,
		Name:        name,
		Format:      "raw",
		Type:        domain.TypeProxy,
		Online:      true,
		ProxyConfig: map[string]any{"remote_url": remoteURL},
	}
}

// ── JoinURL ──────────────────────────────────────────────────────

func TestJoinURL(t *testing.T) {
	cases := []struct {
		base, path, want string
	}{
		{"https://repo.example.com", "/path/to/file.jar", "https://repo.example.com/path/to/file.jar"},
		{"https://repo.example.com/maven2", "/group/artifact/1.0/a.jar", "https://repo.example.com/maven2/group/artifact/1.0/a.jar"},
		{"https://repo.example.com/", "file.txt", "https://repo.example.com/file.txt"},
	}
	for _, tc := range cases {
		got, err := repoproxy.JoinURL(tc.base, tc.path)
		require.NoError(t, err)
		assert.Equal(t, tc.want, got)
	}
}

// ── RejectMutation ───────────────────────────────────────────────

func TestRejectMutation_ProxyPUT(t *testing.T) {
	repo := proxyRepo("p", "https://example.com")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPut, "/", strings.NewReader("x"))
	rejected := repoproxy.RejectMutation(c, repo)
	assert.True(t, rejected)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func TestRejectMutation_HostedPUT(t *testing.T) {
	hosted := &domain.Repository{Type: domain.TypeHosted}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPut, "/", nil)
	rejected := repoproxy.RejectMutation(c, hosted)
	assert.False(t, rejected)
}

// ── ServeGET cache hit ────────────────────────────────────────────

func serveGET(repo *domain.Repository, assetPath, blobContent string) *httptest.ResponseRecorder {
	repos := testutil.NewRepoRepo(repo)
	blobs := testutil.NewBlobStoreRepo()
	comps := testutil.NewComponentRepo()
	assets := testutil.NewAssetRepo()
	blobStore := testutil.NewBlobStore()

	d := formats.Deps{
		Repos: repos, Blobs: blobs, Components: comps,
		Assets: assets, BlobStore: blobStore,
	}

	if blobContent != "" {
		key := "cached-key"
		_ = blobStore.Put(context.TODO(), key, strings.NewReader(blobContent), int64(len(blobContent)))
		a := &domain.Asset{
			Repository:   repo.Name,
			RepositoryID: repo.ID,
			Path:         assetPath,
			BlobKey:      key,
			ContentType:  "application/octet-stream",
			SizeBytes:    int64(len(blobContent)),
		}
		_ = assets.Create(context.TODO(), a)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, assetPath, nil)

	coords := base.Coords{}
	_ = repoproxy.ServeGET(c, d, repo, assetPath, "", coords, "application/octet-stream")
	return w
}

func TestServeGET_CacheHit(t *testing.T) {
	repo := proxyRepo("myproxy", "https://upstream.example.com")
	w := serveGET(repo, "/lib/artifact.jar", "cached content")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "cached content", w.Body.String())
}

func TestServeGET_CacheMiss_FetchesUpstream(t *testing.T) {
	const body = "upstream artifact content"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}))
	defer upstream.Close()

	repo := proxyRepo("cacheproxy", upstream.URL)

	repos := testutil.NewRepoRepo(repo)
	blobs := testutil.NewBlobStoreRepo()
	comps := testutil.NewComponentRepo()
	assets := testutil.NewAssetRepo()
	blobStore := testutil.NewBlobStore()

	d := formats.Deps{
		Repos: repos, Blobs: blobs, Components: comps,
		Assets: assets, BlobStore: blobStore,
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/artifact.bin", nil)

	err := repoproxy.ServeGET(c, d, repo, "/artifact.bin", "", base.Coords{Name: "artifact.bin"}, "application/octet-stream")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, body, w.Body.String())

	// After cache-miss fetch, blob should be persisted
	key := base.BlobKey("cacheproxy", "/artifact.bin")
	assert.True(t, blobStore.Has(key), "blob should be cached after upstream fetch")
}
