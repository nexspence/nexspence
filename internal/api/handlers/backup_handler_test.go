package handlers_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/api/handlers"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func buildBackupHandler(repos ...*domain.Repository) *handlers.BackupHandler {
	svc := &service.BackupService{
		BlobStores: testutil.NewBlobStoreRepo(),
		Repos:      testutil.NewRepoRepo(repos...),
		Users:      testutil.NewUserRepo(),
		Roles:      testutil.NewRoleRepo(),
		Policies:   testutil.NewCleanupPolicyRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
	}
	return handlers.NewBackupHandler(svc)
}

func ginTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	return r
}

func TestBackupHandler_ExportRepo_NotFound(t *testing.T) {
	h := buildBackupHandler(testutil.SimpleRepo("other", "raw"))
	r := ginTestRouter()
	r.GET("/api/v1/repositories/:name/export", h.ExportRepo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/repositories/nonexistent/export", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "not found")
}

func TestBackupHandler_ExportRepo_SetsHeaders(t *testing.T) {
	repo := testutil.SimpleRepo("myrepo", "raw")
	h := buildBackupHandler(repo)
	r := ginTestRouter()
	r.GET("/api/v1/repositories/:name/export", h.ExportRepo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/repositories/myrepo/export", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Disposition"), "attachment")
	assert.Contains(t, w.Header().Get("Content-Disposition"), "myrepo")
	assert.Equal(t, "application/x-tar", w.Header().Get("Content-Type"))
	assert.Greater(t, w.Body.Len(), 0)
}

func TestBackupHandler_ImportRepo_MissingFile(t *testing.T) {
	h := buildBackupHandler()
	r := ginTestRouter()
	r.POST("/api/v1/repositories/import", h.ImportRepo)

	// No "file" field in the multipart form → 400.
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	require.NoError(t, mw.WriteField("conflictMode", "skip"))
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/repositories/import", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "missing file field")
}

func TestBackupHandler_ImportRepo_ParsesMultipartFields(t *testing.T) {
	// Build a minimal valid gzip/tar archive containing a repository.json.
	var archiveBuf bytes.Buffer
	gw := gzip.NewWriter(&archiveBuf)
	// We don't need a valid tar for this test — just a valid gzip so the service
	// doesn't reject it immediately. We use a gzip-wrapped empty stream and expect
	// "invalid archive" from the service (not a parse error), which means the
	// multipart parsing succeeded and the body was forwarded correctly.
	gw.Close()
	archiveBytes := archiveBuf.Bytes()

	h := buildBackupHandler()
	r := ginTestRouter()
	r.POST("/api/v1/repositories/import", h.ImportRepo)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "archive.tar.gz")
	require.NoError(t, err)
	_, err = io.Copy(fw, bytes.NewReader(archiveBytes))
	require.NoError(t, err)
	require.NoError(t, mw.WriteField("targetName", "importedrepo"))
	require.NoError(t, mw.WriteField("conflictMode", "skip"))
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/repositories/import", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Archive has empty content — service returns "invalid archive" (400), not a 5xx.
	// This confirms multipart parsing reached the service with the correct reader.
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid archive")
}

// Ensure context cancellation is handled without panic in ExportRepo.
func TestBackupHandler_ExportRepo_ContextCancelled(t *testing.T) {
	repo := testutil.SimpleRepo("myrepo", "raw")
	h := buildBackupHandler(repo)
	r := ginTestRouter()
	r.GET("/api/v1/repositories/:name/export", h.ExportRepo)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	req := httptest.NewRequest(http.MethodGet, "/api/v1/repositories/myrepo/export", nil).
		WithContext(ctx)
	w := httptest.NewRecorder()

	// Should not panic even with a canceled context.
	assert.NotPanics(t, func() {
		r.ServeHTTP(w, req)
	})
}
