package npm_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/npm"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// ─── helpers ────────────────────────────────────────────────────

// proxySetup builds a gin engine backed by a proxy repo pointing at upstream.
func proxySetup(upstream *httptest.Server) (*gin.Engine, *domain.Repository) {
	repo := &domain.Repository{
		ID:     "npm-proxy",
		Name:   "npm-proxy",
		Format: "npm",
		Type:   domain.TypeProxy,
		Online: true,
		ProxyConfig: map[string]any{
			"remote_url": upstream.URL,
		},
	}
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	h := npm.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	return r, repo
}

// ─── Name() ─────────────────────────────────────────────────────

func TestNPM_Name(t *testing.T) {
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
	}
	h := npm.New(d)
	assert.Equal(t, "npm", h.Name())
}

// ─── ServeHTTP: DELETE ──────────────────────────────────────────

func TestNPM_Delete_Hosted(t *testing.T) {
	repo := testutil.SimpleRepo("npm-del", "npm")
	r := setup(repo)

	// Publish something first
	body := publishBody("todelete", "1.0.0", "data")
	req := httptest.NewRequest(http.MethodPut, "/repository/npm-del/todelete",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(httptest.NewRecorder(), req)

	// Now delete
	req2 := httptest.NewRequest(http.MethodDelete, "/repository/npm-del/todelete", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req2)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"ok"`)
}

func TestNPM_Delete_ProxyRejected(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	r, _ := proxySetup(upstream)
	req := httptest.NewRequest(http.MethodDelete, "/repository/npm-proxy/pkg", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// ─── ServeHTTP: method not allowed ─────────────────────────────

func TestNPM_MethodNotAllowed(t *testing.T) {
	repo := testutil.SimpleRepo("npm-mna", "npm")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodPost, "/repository/npm-mna/somepkg", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// ─── serveTarball ───────────────────────────────────────────────

func TestNPM_Tarball_HEAD(t *testing.T) {
	repo := testutil.SimpleRepo("npm-head", "npm")
	r := setup(repo)

	// Publish first
	body := publishBody("headpkg", "2.0.0", "tarball-bytes")
	req := httptest.NewRequest(http.MethodPut, "/repository/npm-head/headpkg",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(httptest.NewRecorder(), req)

	// HEAD
	req2 := httptest.NewRequest(http.MethodHead, "/repository/npm-head/headpkg/-/headpkg-2.0.0.tgz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req2)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.NotEmpty(t, w.Header().Get("Content-Length"))
	// HEAD should not have a body
	assert.Empty(t, w.Body.String())
}

func TestNPM_Tarball_NotFound(t *testing.T) {
	repo := testutil.SimpleRepo("npm-tf", "npm")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet, "/repository/npm-tf/mypkg/-/mypkg-9.9.9.tgz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestNPM_Tarball_Proxy_Cached(t *testing.T) {
	// Upstream serves the tarball
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".tgz") {
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write([]byte("tgz-from-upstream"))
			return
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()

	r, _ := proxySetup(upstream)
	req := httptest.NewRequest(http.MethodGet, "/repository/npm-proxy/mylib/-/mylib-1.0.0.tgz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	// Proxy should return the upstream content (200) or at worst a gateway error — not 404
	assert.NotEqual(t, http.StatusNotFound, w.Code)
}

func TestNPM_Tarball_Proxy_RepoGetError(t *testing.T) {
	// Repo repo that returns an error
	repoRepo := testutil.NewRepoRepo()
	repoRepo.Err = assert.AnError
	d := formats.Deps{
		Repos:      repoRepo,
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	h := npm.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })

	req := httptest.NewRequest(http.MethodGet, "/repository/bad-repo/pkg/-/pkg-1.0.0.tgz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// ─── serveMetadata proxy ────────────────────────────────────────

func TestNPM_Metadata_Proxy(t *testing.T) {
	upstreamMeta := map[string]any{
		"name":      "proxypkg",
		"dist-tags": map[string]string{"latest": "3.0.0"},
		"versions": map[string]any{
			"3.0.0": map[string]any{"name": "proxypkg", "version": "3.0.0"},
		},
	}
	metaBytes, _ := json.Marshal(upstreamMeta)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(metaBytes)
	}))
	defer upstream.Close()

	r, _ := proxySetup(upstream)

	req := httptest.NewRequest(http.MethodGet, "/repository/npm-proxy/proxypkg", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	// Should return the upstream JSON (200) — may be served from cache or forwarded
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestNPM_Metadata_Proxy_RepoGetError(t *testing.T) {
	repoRepo := testutil.NewRepoRepo()
	repoRepo.Err = assert.AnError
	d := formats.Deps{
		Repos:      repoRepo,
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	h := npm.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })

	req := httptest.NewRequest(http.MethodGet, "/repository/bad-repo/somepkg", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// ─── handlePublish error paths ──────────────────────────────────

func TestNPM_Publish_InvalidJSON(t *testing.T) {
	repo := testutil.SimpleRepo("npm-inv", "npm")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodPut, "/repository/npm-inv/pkg",
		strings.NewReader("not-json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestNPM_Publish_VersionFromVersionsMap(t *testing.T) {
	// dist-tags absent; version resolved from versions map
	repo := testutil.SimpleRepo("npm-vmap", "npm")
	r := setup(repo)

	encoded := base64.StdEncoding.EncodeToString([]byte("content"))
	body := map[string]any{
		"name": "vmap-pkg",
		"versions": map[string]any{
			"5.0.0": map[string]any{"name": "vmap-pkg", "version": "5.0.0"},
		},
		"_attachments": map[string]any{
			"vmap-pkg-5.0.0.tgz": map[string]any{
				"data":         encoded,
				"content_type": "application/octet-stream",
				"length":       7,
			},
		},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/repository/npm-vmap/vmap-pkg",
		strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestNPM_Publish_NoVersionAnywhere_Returns400(t *testing.T) {
	repo := testutil.SimpleRepo("npm-nover", "npm")
	r := setup(repo)

	body := `{"name":"pkg","_attachments":{"f.tgz":{"data":"","length":0}}}`
	req := httptest.NewRequest(http.MethodPut, "/repository/npm-nover/pkg",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestNPM_Publish_InvalidBase64_Returns400(t *testing.T) {
	repo := testutil.SimpleRepo("npm-b64", "npm")
	r := setup(repo)

	body := map[string]any{
		"name":      "pkg",
		"dist-tags": map[string]string{"latest": "1.0.0"},
		"_attachments": map[string]any{
			"pkg-1.0.0.tgz": map[string]any{
				"data":   "!!!not-base64!!!",
				"length": 0,
			},
		},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, "/repository/npm-b64/pkg",
		strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestNPM_Publish_InvalidAttachments_Returns400(t *testing.T) {
	// _attachments is not an object
	repo := testutil.SimpleRepo("npm-invatt", "npm")
	r := setup(repo)

	body := `{"name":"pkg","dist-tags":{"latest":"1.0.0"},"_attachments":"not-an-object"}`
	req := httptest.NewRequest(http.MethodPut, "/repository/npm-invatt/pkg",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestNPM_Publish_ScopedPackage(t *testing.T) {
	repo := testutil.SimpleRepo("npm-scoped", "npm")
	r := setup(repo)

	encoded := base64.StdEncoding.EncodeToString([]byte("scoped-content"))
	body := map[string]any{
		"name":      "@myorg/mypkg",
		"dist-tags": map[string]string{"latest": "1.0.0"},
		"versions": map[string]any{
			"1.0.0": map[string]any{"name": "@myorg/mypkg", "version": "1.0.0"},
		},
		"_attachments": map[string]any{
			"mypkg-1.0.0.tgz": map[string]any{
				"data":         encoded,
				"content_type": "application/octet-stream",
				"length":       14,
			},
		},
	}
	b, _ := json.Marshal(body)

	// Scoped packages come in as @scope/name in the URL
	req := httptest.NewRequest(http.MethodPut, "/repository/npm-scoped/@myorg%2Fmypkg",
		strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)
}

// ─── serveMetadata: Search error ────────────────────────────────

func TestNPM_Metadata_SearchError(t *testing.T) {
	repo := testutil.SimpleRepo("npm-serr", "npm")
	compRepo := testutil.NewComponentRepo()
	compRepo.Err = assert.AnError
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: compRepo,
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	h := npm.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })

	req := httptest.NewRequest(http.MethodGet, "/repository/npm-serr/somepkg", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// ─── DELETE: Repo Get error ──────────────────────────────────────

func TestNPM_Delete_RepoGetError(t *testing.T) {
	repoRepo := testutil.NewRepoRepo()
	repoRepo.Err = assert.AnError
	d := formats.Deps{
		Repos:      repoRepo,
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	h := npm.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })

	req := httptest.NewRequest(http.MethodDelete, "/repository/bad-repo/pkg", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// ─── PUT: Repo Get error ─────────────────────────────────────────

func TestNPM_Publish_RepoGetError(t *testing.T) {
	repoRepo := testutil.NewRepoRepo()
	repoRepo.Err = assert.AnError
	d := formats.Deps{
		Repos:      repoRepo,
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	h := npm.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })

	body := publishBody("pkg", "1.0.0", "data")
	req := httptest.NewRequest(http.MethodPut, "/repository/bad-repo/pkg",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
