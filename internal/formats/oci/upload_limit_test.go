package oci_test

import (
	"bytes"
	"encoding/json"
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
	"github.com/nexspence-oss/nexspence/internal/formats/oci"
	"github.com/nexspence-oss/nexspence/internal/requestctx"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// setupWithUploadLimit wires a hosted docker repository whose blob uploads are
// capped at maxUploadBytes (0 = uncapped).
func setupWithUploadLimit(maxUploadBytes int64) *gin.Engine {
	repo := &domain.Repository{
		ID: "repo-limit", Name: "docker-hosted",
		Format: domain.FormatDocker, Type: domain.TypeHosted, Online: true,
	}
	d := formats.Deps{
		Repos:          testutil.NewRepoRepo(repo),
		Blobs:          testutil.NewBlobStoreRepo(),
		Components:     testutil.NewComponentRepo(),
		Assets:         testutil.NewAssetRepo(),
		BlobStore:      testutil.NewBlobStore(),
		BaseURL:        "http://localhost:8080",
		MaxUploadBytes: maxUploadBytes,
	}
	h := oci.New(d)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		ctx := requestctx.WithUser(c.Request.Context(), "test-user-id", "testuser")
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	return r
}

// startUpload initiates a blob upload and returns the upload location.
func startUpload(t *testing.T, r *gin.Engine) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost,
		"/repository/docker-hosted/v2/app/blobs/uploads/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusAccepted, w.Code, "initiate upload: %s", w.Body.String())
	loc := w.Header().Get("Location")
	require.NotEmpty(t, loc)
	return loc
}

func patchChunk(t *testing.T, r *gin.Engine, location string, size int) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPatch, location,
		bytes.NewReader(bytes.Repeat([]byte("x"), size)))
	req.Header.Set("Content-Type", "application/octet-stream")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func dockerErrorCode(t *testing.T, body string) string {
	t.Helper()
	var payload struct {
		Errors []struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &payload), "body: %s", body)
	require.NotEmpty(t, payload.Errors, "body: %s", body)
	return payload.Errors[0].Code
}

func TestPatchUploadAcceptsChunkUnderLimit(t *testing.T) {
	r := setupWithUploadLimit(1024)
	loc := startUpload(t, r)

	w := patchChunk(t, r, loc, 512)

	assert.Equal(t, http.StatusAccepted, w.Code, "body: %s", w.Body.String())
	assert.Equal(t, "0-511", w.Header().Get("Range"))
}

func TestPatchUploadRejectsChunkOverLimit(t *testing.T) {
	r := setupWithUploadLimit(1024)
	loc := startUpload(t, r)

	w := patchChunk(t, r, loc, 4096)

	require.Equal(t, http.StatusRequestEntityTooLarge, w.Code, "body: %s", w.Body.String())
	assert.Equal(t, "BLOB_UPLOAD_INVALID", dockerErrorCode(t, w.Body.String()))
}

func TestPatchUploadRejectsChunkThatCrossesLimitAndKeepsSessionIntact(t *testing.T) {
	r := setupWithUploadLimit(1024)
	loc := startUpload(t, r)

	first := patchChunk(t, r, loc, 800)
	require.Equal(t, http.StatusAccepted, first.Code, "body: %s", first.Body.String())

	second := patchChunk(t, r, loc, 800)
	require.Equal(t, http.StatusRequestEntityTooLarge, second.Code, "body: %s", second.Body.String())

	// The rejected chunk must not have been staged: the session still holds
	// exactly the 800 bytes the accepted chunk wrote, so the remaining budget
	// is 224 bytes — no more, no less.
	assert.Equal(t, "0-799", first.Header().Get("Range"))

	third := patchChunk(t, r, loc, 224)
	require.Equal(t, http.StatusAccepted, third.Code,
		"rejected chunk consumed budget it never used: %s", third.Body.String())
	assert.Equal(t, "0-1023", third.Header().Get("Range"))

	fourth := patchChunk(t, r, loc, 1)
	assert.Equal(t, http.StatusRequestEntityTooLarge, fourth.Code,
		"session at the cap accepted more bytes: %s", fourth.Body.String())
}

func TestUploadLimitDisabledAcceptsLargeChunk(t *testing.T) {
	r := setupWithUploadLimit(0)
	loc := startUpload(t, r)

	w := patchChunk(t, r, loc, 4096)

	assert.Equal(t, http.StatusAccepted, w.Code, "body: %s", w.Body.String())
	assert.Equal(t, "0-4095", w.Header().Get("Range"))
}

func TestFinalizeUploadRejectsBodyOverLimit(t *testing.T) {
	r := setupWithUploadLimit(1024)
	loc := startUpload(t, r)

	body := bytes.Repeat([]byte("y"), 4096)
	sep := "?"
	if strings.Contains(loc, "?") {
		sep = "&"
	}
	req := httptest.NewRequest(http.MethodPut,
		fmt.Sprintf("%s%sdigest=%s", loc, sep, digest(string(body))),
		bytes.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusRequestEntityTooLarge, w.Code, "body: %s", w.Body.String())
	assert.Equal(t, "BLOB_UPLOAD_INVALID", dockerErrorCode(t, w.Body.String()))
}
