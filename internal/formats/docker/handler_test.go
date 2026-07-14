package docker_test

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/docker"
	"github.com/nexspence-oss/nexspence/internal/requestctx"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func init() { gin.SetMode(gin.TestMode) }

func setup(repo *domain.Repository) *gin.Engine {
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	h := docker.New(d)
	r := gin.New()
	// Inject a user into context so requireDockerAuth passes.
	r.Use(func(c *gin.Context) {
		ctx := requestctx.WithUser(c.Request.Context(), "test-user-id", "testuser")
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	return r
}

func digest(content string) string {
	sum := sha256.Sum256([]byte(content))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// pushBlob performs the three-step OCI blob upload: POST → PATCH → PUT.
// Returns the digest of the uploaded content.
func pushBlob(t *testing.T, r *gin.Engine, repoName, imageName, content string) string {
	t.Helper()
	dgst := digest(content)

	// POST: initiate upload
	req := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/repository/%s/v2/%s/blobs/uploads/", repoName, imageName), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusAccepted, w.Code, "POST blob upload should return 202")
	location := w.Header().Get("Location")
	require.NotEmpty(t, location, "Location header required")

	// PATCH: stream data
	req2 := httptest.NewRequest(http.MethodPatch, location, strings.NewReader(content))
	req2.ContentLength = int64(len(content))
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	require.Equal(t, http.StatusAccepted, w2.Code, "PATCH should return 202")

	// PUT: finalize
	req3 := httptest.NewRequest(http.MethodPut, location+"?digest="+dgst, nil)
	w3 := httptest.NewRecorder()
	r.ServeHTTP(w3, req3)
	require.Equal(t, http.StatusCreated, w3.Code, "PUT finalize should return 201")
	return dgst
}

// setupV2Scoped mimics production routing (short-path /v2/:repoName/*) where the
// Docker client only sends its credentials to requests under /v2/. It reproduces
// issue #47: if the blob-upload Location escapes the /v2/ prefix, the finalize
// PUT arrives anonymous and gets 401.
func setupV2Scoped(repo *domain.Repository) *gin.Engine {
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	h := docker.New(d)
	r := gin.New()
	// Credentials are only attached to /v2/ requests (as a real Docker client does).
	r.Use(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/v2/") {
			ctx := requestctx.WithUser(c.Request.Context(), "test-user-id", "testuser")
			c.Request = c.Request.WithContext(ctx)
		}
		c.Next()
	})
	dispatch := func(c *gin.Context) {
		c.Params = gin.Params{
			{Key: "repoName", Value: c.Param("repoName")},
			{Key: "path", Value: "/v2" + c.Param("dockerpath")},
		}
		h.ServeHTTP(c)
	}
	r.Any("/v2/:repoName/*dockerpath", dispatch)
	return r
}

func TestDocker_ShortPathPush_StaysAuthenticated(t *testing.T) {
	repo := testutil.SimpleRepo("da", "docker")
	r := setupV2Scoped(repo)
	content := "layer-bytes"
	dgst := digest(content)

	// POST initiate — the client authenticated against /v2/.
	req := httptest.NewRequest(http.MethodPost, "/v2/da/devops/alpine/blobs/uploads/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusAccepted, w.Code)
	loc := w.Header().Get("Location")
	require.True(t, strings.HasPrefix(loc, "/v2/"),
		"Location must stay under /v2/ so the client keeps sending credentials, got %q", loc)

	// PATCH the blob body — must not 401.
	req2 := httptest.NewRequest(http.MethodPatch, loc, strings.NewReader(content))
	req2.ContentLength = int64(len(content))
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	require.Equal(t, http.StatusAccepted, w2.Code, "PATCH must stay authenticated")

	// PUT finalize — this is where #47 returned 401 at 100%.
	req3 := httptest.NewRequest(http.MethodPut, loc+"?digest="+dgst, nil)
	w3 := httptest.NewRecorder()
	r.ServeHTTP(w3, req3)
	require.Equal(t, http.StatusCreated, w3.Code, "finalize PUT must stay authenticated (issue #47)")
}

// ── Version check ─────────────────────────────────────────────

func TestDocker_VersionCheck(t *testing.T) {
	repo := testutil.SimpleRepo("reg", "docker")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet, "/repository/reg/v2/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "registry/2.0", w.Header().Get("Docker-Distribution-API-Version"))
}

// ── Blob push/pull ────────────────────────────────────────────

func TestDocker_BlobPushPull(t *testing.T) {
	repo := testutil.SimpleRepo("reg2", "docker")
	r := setup(repo)

	content := "layer data"
	dgst := pushBlob(t, r, "reg2", "library/alpine", content)

	// GET blob
	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/repository/reg2/v2/library/alpine/blobs/%s", dgst), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, content, w.Body.String())
	assert.Equal(t, dgst, w.Header().Get("Docker-Content-Digest"))
}

func TestDocker_BlobHead(t *testing.T) {
	repo := testutil.SimpleRepo("reg3", "docker")
	r := setup(repo)

	content := "layer-x"
	dgst := pushBlob(t, r, "reg3", "myimage", content)

	req := httptest.NewRequest(http.MethodHead,
		fmt.Sprintf("/repository/reg3/v2/myimage/blobs/%s", dgst), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, w.Body.String())
}

func TestDocker_BlobNotFound(t *testing.T) {
	repo := testutil.SimpleRepo("reg4", "docker")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/reg4/v2/alpine/blobs/sha256:deadbeef", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ── Manifest push/pull ────────────────────────────────────────

func TestDocker_ManifestPushPull(t *testing.T) {
	repo := testutil.SimpleRepo("reg5", "docker")
	r := setup(repo)

	manifest := `{"schemaVersion":2,"mediaType":"application/vnd.docker.distribution.manifest.v2+json"}`

	// PUT manifest
	req := httptest.NewRequest(http.MethodPut,
		"/repository/reg5/v2/library/ubuntu/manifests/latest",
		strings.NewReader(manifest))
	req.Header.Set("Content-Type", "application/vnd.docker.distribution.manifest.v2+json")
	req.ContentLength = int64(len(manifest))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusCreated, w.Code)
	assert.NotEmpty(t, w.Header().Get("Docker-Content-Digest"))

	// GET manifest
	req2 := httptest.NewRequest(http.MethodGet,
		"/repository/reg5/v2/library/ubuntu/manifests/latest", nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusOK, w2.Code)
	assert.Equal(t, manifest, w2.Body.String())
}

// ── Tags list ─────────────────────────────────────────────────

func TestDocker_TagsList_Empty(t *testing.T) {
	repo := testutil.SimpleRepo("reg6", "docker")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/reg6/v2/myapp/tags/list", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"tags"`)
}

func TestDocker_TagsList_AfterPush(t *testing.T) {
	repo := testutil.SimpleRepo("reg7", "docker")
	r := setup(repo)

	manifest := `{"schemaVersion":2}`
	for _, tag := range []string{"v1.0", "v2.0", "latest"} {
		req := httptest.NewRequest(http.MethodPut,
			"/repository/reg7/v2/myapp/manifests/"+tag,
			strings.NewReader(manifest))
		req.ContentLength = int64(len(manifest))
		r.ServeHTTP(httptest.NewRecorder(), req)
	}

	req := httptest.NewRequest(http.MethodGet, "/repository/reg7/v2/myapp/tags/list", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "v1.0")
	assert.Contains(t, body, "v2.0")
	assert.Contains(t, body, "latest")
}

// ── Auth gate ─────────────────────────────────────────────────

func TestDocker_BlobUpload_NoAuth_Returns401(t *testing.T) {
	repo := testutil.SimpleRepo("reg8", "docker")
	d := formats.Deps{
		Repos: testutil.NewRepoRepo(repo), Blobs: testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(), Assets: testutil.NewAssetRepo(),
		BlobStore: testutil.NewBlobStore(),
	}
	h := docker.New(d)
	r := gin.New()
	// No user injected — requireDockerAuth should challenge with 401
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })

	req := httptest.NewRequest(http.MethodPost,
		"/repository/reg8/v2/img/blobs/uploads/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
