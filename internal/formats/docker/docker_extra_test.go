package docker_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/docker"
	"github.com/nexspence-oss/nexspence/internal/requestctx"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// ── Name ──────────────────────────────────────────────────────

func TestDocker_Name(t *testing.T) {
	repo := testutil.SimpleRepo("dname", "docker")
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	h := docker.New(d)
	assert.Equal(t, "docker", h.Name())
}

// ── Version check (no /v2/ prefix) ───────────────────────────

func TestDocker_NoV2Prefix_NotFound(t *testing.T) {
	repo := testutil.SimpleRepo("dreg-nov2", "docker")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet, "/repository/dreg-nov2/other/path", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ── Tags list method not allowed ─────────────────────────────

func TestDocker_TagsList_MethodNotAllowed(t *testing.T) {
	repo := testutil.SimpleRepo("dtags-method", "docker")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodPut, "/repository/dtags-method/v2/myapp/tags/list", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// ── Manifest delete ───────────────────────────────────────────

func TestDocker_ManifestDelete(t *testing.T) {
	repo := testutil.SimpleRepo("dreg-del", "docker")
	r := setup(repo)

	manifest := `{"schemaVersion":2}`
	// First push a manifest
	req := httptest.NewRequest(http.MethodPut,
		"/repository/dreg-del/v2/library/alpine/manifests/v1.0",
		strings.NewReader(manifest))
	req.ContentLength = int64(len(manifest))
	r.ServeHTTP(httptest.NewRecorder(), req)

	// Then delete it
	req2 := httptest.NewRequest(http.MethodDelete,
		"/repository/dreg-del/v2/library/alpine/manifests/v1.0", nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusAccepted, w2.Code)
}

// TestDocker_ManifestDelete_OnMissing covers deleteManifest when artifact doesn't exist.
func TestDocker_ManifestDelete_OnMissing(t *testing.T) {
	repo := testutil.SimpleRepo("dreg-del-miss", "docker")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodDelete,
		"/repository/dreg-del-miss/v2/library/alpine/manifests/nonexistent", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	// DeleteArtifact on unknown path: 500 (mock returns error) or 202 if silent
	assert.True(t, w.Code == http.StatusAccepted || w.Code == http.StatusInternalServerError)
}

// ── Manifest method not allowed ───────────────────────────────

func TestDocker_ManifestMethod_NotAllowed(t *testing.T) {
	repo := testutil.SimpleRepo("dreg-meth", "docker")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodPatch,
		"/repository/dreg-meth/v2/library/alpine/manifests/latest", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// ── Manifest pull (HEAD) ───────────────────────────────────────

func TestDocker_ManifestHead(t *testing.T) {
	repo := testutil.SimpleRepo("dreg-mhead", "docker")
	r := setup(repo)

	manifest := `{"schemaVersion":2}`
	req := httptest.NewRequest(http.MethodPut,
		"/repository/dreg-mhead/v2/myimage/manifests/latest",
		strings.NewReader(manifest))
	req.ContentLength = int64(len(manifest))
	r.ServeHTTP(httptest.NewRecorder(), req)

	req2 := httptest.NewRequest(http.MethodHead,
		"/repository/dreg-mhead/v2/myimage/manifests/latest", nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusOK, w2.Code)
	assert.Empty(t, w2.Body.String())
}

// ── Manifest pull not found ───────────────────────────────────

func TestDocker_ManifestPull_NotFound(t *testing.T) {
	repo := testutil.SimpleRepo("dreg-mnf", "docker")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/dreg-mnf/v2/library/alpine/manifests/nonexistent", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ── Blob delete ───────────────────────────────────────────────

func TestDocker_BlobDelete(t *testing.T) {
	repo := testutil.SimpleRepo("dreg-bdel", "docker")
	r := setup(repo)

	content := "layer-for-delete"
	dgst := pushBlob(t, r, "dreg-bdel", "myimage", content)

	req := httptest.NewRequest(http.MethodDelete,
		"/repository/dreg-bdel/v2/myimage/blobs/"+dgst, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusAccepted, w.Code)
}

// ── Blob HEAD not found ───────────────────────────────────────

func TestDocker_BlobHead_NotFound(t *testing.T) {
	repo := testutil.SimpleRepo("dreg-bnf", "docker")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodHead,
		"/repository/dreg-bnf/v2/myimage/blobs/sha256:deadbeef", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ── Blob method not allowed ───────────────────────────────────

func TestDocker_BlobMethod_NotAllowed(t *testing.T) {
	repo := testutil.SimpleRepo("dreg-bmeth", "docker")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodPut,
		"/repository/dreg-bmeth/v2/myimage/blobs/sha256:abc123", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// ── Blob upload: PATCH unknown session ──────────────────────────

func TestDocker_BlobUpload_Patch_UnknownSession(t *testing.T) {
	repo := testutil.SimpleRepo("dreg-patch-unk", "docker")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodPatch,
		"/repository/dreg-patch-unk/v2/myimage/blobs/uploads/no-such-uuid",
		strings.NewReader("data"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ── Blob upload: PUT missing uuid ────────────────────────────────

func TestDocker_BlobUpload_Put_MissingUUID(t *testing.T) {
	repo := testutil.SimpleRepo("dreg-put-noid", "docker")
	r := setup(repo)

	// PUT to the uploads root (no uuid)
	req := httptest.NewRequest(http.MethodPut,
		"/repository/dreg-put-noid/v2/myimage/blobs/uploads/",
		strings.NewReader("data"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ── Blob upload: PATCH missing uuid ─────────────────────────────

func TestDocker_BlobUpload_Patch_MissingUUID(t *testing.T) {
	repo := testutil.SimpleRepo("dreg-patch-noid", "docker")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodPatch,
		"/repository/dreg-patch-noid/v2/myimage/blobs/uploads/",
		strings.NewReader("data"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ── Blob upload: GET progress ─────────────────────────────────

func TestDocker_BlobUpload_GetProgress(t *testing.T) {
	repo := testutil.SimpleRepo("dreg-prog", "docker")
	r := setup(repo)

	// Start an upload to get a UUID
	req := httptest.NewRequest(http.MethodPost,
		"/repository/dreg-prog/v2/myimage/blobs/uploads/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusAccepted, w.Code)
	loc := w.Header().Get("Location")
	require.NotEmpty(t, loc)

	// Query progress
	req2 := httptest.NewRequest(http.MethodGet, loc, nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusNoContent, w2.Code)
	assert.NotEmpty(t, w2.Header().Get("Docker-Upload-UUID"))
}

// ── Blob upload: GET progress unknown session ─────────────────

func TestDocker_BlobUpload_GetProgress_UnknownSession(t *testing.T) {
	repo := testutil.SimpleRepo("dreg-prog-unk", "docker")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/dreg-prog-unk/v2/myimage/blobs/uploads/no-such-uuid", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ── Blob upload: GET no uuid ──────────────────────────────────

func TestDocker_BlobUpload_GetNoUUID(t *testing.T) {
	repo := testutil.SimpleRepo("dreg-get-noid", "docker")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/dreg-get-noid/v2/myimage/blobs/uploads/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ── Blob upload: PUT no digest ───────────────────────────────

func TestDocker_BlobUpload_Finalize_NoDigest(t *testing.T) {
	repo := testutil.SimpleRepo("dreg-nodig", "docker")
	r := setup(repo)

	// Start upload
	req := httptest.NewRequest(http.MethodPost,
		"/repository/dreg-nodig/v2/myimage/blobs/uploads/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusAccepted, w.Code)
	loc := w.Header().Get("Location")

	// PUT without digest query param
	req2 := httptest.NewRequest(http.MethodPut, loc, nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusBadRequest, w2.Code)
}

// ── Blob upload: PUT unknown session ──────────────────────────

func TestDocker_BlobUpload_Finalize_UnknownSession(t *testing.T) {
	repo := testutil.SimpleRepo("dreg-fin-unk", "docker")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodPut,
		"/repository/dreg-fin-unk/v2/myimage/blobs/uploads/no-such-uuid?digest=sha256:abc",
		nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ── Blob upload: method not allowed ─────────────────────────

func TestDocker_BlobUploads_MethodNotAllowed(t *testing.T) {
	repo := testutil.SimpleRepo("dreg-upl-meth", "docker")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodDelete,
		"/repository/dreg-upl-meth/v2/myimage/blobs/uploads/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// ── Default path (not tags/manifests/blobs) ───────────────────

func TestDocker_UnknownRoute_NotFound(t *testing.T) {
	repo := testutil.SimpleRepo("dreg-unk", "docker")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/dreg-unk/v2/myimage/something/unknown", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ── Proxy GET (manifest) ──────────────────────────────────────

func TestDocker_ProxyManifest_Get_FallsThrough(t *testing.T) {
	repo := testutil.SimpleRepo("dreg-proxy-m", "docker")
	repo.Type = "proxy"
	repo.ProxyConfig = map[string]any{"remote_url": "http://127.0.0.1:1"}

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
	r.Use(func(c *gin.Context) {
		ctx := requestctx.WithUser(c.Request.Context(), "test-user-id", "testuser")
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })

	req := httptest.NewRequest(http.MethodGet,
		"/repository/dreg-proxy-m/v2/library/alpine/manifests/latest", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	// Proxy branch taken; upstream unreachable → non-200
	assert.NotEqual(t, http.StatusOK, w.Code)
}

// ── Proxy GET (blob) ──────────────────────────────────────────

func TestDocker_ProxyBlob_Get_FallsThrough(t *testing.T) {
	repo := testutil.SimpleRepo("dreg-proxy-b", "docker")
	repo.Type = "proxy"
	repo.ProxyConfig = map[string]any{"remote_url": "http://127.0.0.1:1"}

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
	r.Use(func(c *gin.Context) {
		ctx := requestctx.WithUser(c.Request.Context(), "test-user-id", "testuser")
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })

	req := httptest.NewRequest(http.MethodGet,
		"/repository/dreg-proxy-b/v2/library/alpine/blobs/sha256:deadbeef", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	// Proxy branch taken; upstream unreachable → non-200
	assert.NotEqual(t, http.StatusOK, w.Code)
}

// ── Monolithic PUT (body included in finalize) ────────────────

func TestDocker_BlobUpload_MonolithicPut(t *testing.T) {
	repo := testutil.SimpleRepo("dreg-mono", "docker")
	r := setup(repo)

	content := "monolithic-blob-data"
	dgst := digest(content)

	// POST to initiate
	req := httptest.NewRequest(http.MethodPost,
		"/repository/dreg-mono/v2/myimage/blobs/uploads/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusAccepted, w.Code)
	loc := w.Header().Get("Location")

	// PUT with body (monolithic upload — ContentLength > 0)
	req2 := httptest.NewRequest(http.MethodPut, loc+"?digest="+dgst,
		strings.NewReader(content))
	req2.ContentLength = int64(len(content))
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusCreated, w2.Code)
}

// ── Short parts → 400 ─────────────────────────────────────────

func TestDocker_ShortParts_BadRequest(t *testing.T) {
	repo := testutil.SimpleRepo("dreg-short", "docker")
	r := setup(repo)

	// /v2/ followed by just one segment → parts has len 1
	req := httptest.NewRequest(http.MethodGet,
		"/repository/dreg-short/v2/onlyone", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}
