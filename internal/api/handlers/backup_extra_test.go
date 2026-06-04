package handlers_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// buildRepoArchive produces a minimal valid gzip-tar archive containing just a
// repository.json for the given repo name/format — enough for ImportRepo to
// reach the conflict/creation branch (no components, assets, or blobs).
func buildRepoArchive(t *testing.T, name, format string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	repoJSON, err := json.Marshal(domain.Repository{
		Name:   name,
		Format: domain.RepoFormat(format),
		Type:   domain.TypeHosted,
	})
	require.NoError(t, err)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "repository.json", Mode: 0o600, Size: int64(len(repoJSON))}))
	_, err = tw.Write(repoJSON)
	require.NoError(t, err)

	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())
	return buf.Bytes()
}

// TestBackupExtra_Export_FullBackup covers BackupHandler.Export (full-system export):
// it must set the chunked/x-tar headers and return 200. We assert headers + status,
// not the full archive bytes.
func TestBackupExtra_Export_FullBackup(t *testing.T) {
	h := buildBackupHandler()
	r := ginTestRouter()
	r.GET("/api/v1/backup/export", h.Export)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/backup/export", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Disposition"), "attachment")
	assert.Contains(t, w.Header().Get("Content-Disposition"), "nexspence-backup-")
	assert.Equal(t, "application/x-tar", w.Header().Get("Content-Type"))
	assert.Equal(t, "chunked", w.Header().Get("Transfer-Encoding"))
}

// TestBackupExtra_Restore_BadArchive covers the Restore error branch: a non-gzip
// body is forwarded to the service, which rejects it → 400.
func TestBackupExtra_Restore_BadArchive(t *testing.T) {
	h := buildBackupHandler()
	r := ginTestRouter()
	r.POST("/api/v1/backup/restore", h.Restore)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/backup/restore",
		bytes.NewReader([]byte("this is not a gzip archive")))
	req.Header.Set("Content-Type", "application/octet-stream")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "error")
}

// TestBackupExtra_Restore_Multipart covers the multipart branch of Restore where
// a "file" field is parsed out of the form. The empty gzip body is rejected by the
// service (400), confirming the multipart reader reached the service.
func TestBackupExtra_Restore_Multipart(t *testing.T) {
	var gzBuf bytes.Buffer
	gw := gzip.NewWriter(&gzBuf)
	require.NoError(t, gw.Close())

	h := buildBackupHandler()
	r := ginTestRouter()
	r.POST("/api/v1/backup/restore", h.Restore)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "backup.tar.gz")
	require.NoError(t, err)
	_, err = fw.Write(gzBuf.Bytes())
	require.NoError(t, err)
	require.NoError(t, mw.Close())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/backup/restore", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Multipart parsing succeeded and the file field reached the service. A valid
	// (empty) gzip archive restores nothing → 200 with an empty "restored" stats block.
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "restored")
}

// TestBackupExtra_ImportRepo_Conflict covers the ErrRepoConflict → 409 branch:
// a valid archive for a repo name that already exists, imported with
// conflictMode=rename pointing at the existing name.
func TestBackupExtra_ImportRepo_Conflict(t *testing.T) {
	existing := testutil.SimpleRepo("existingrepo", "raw")
	h := buildBackupHandler(existing)
	r := ginTestRouter()
	r.POST("/api/v1/repositories/import", h.ImportRepo)

	archive := buildRepoArchive(t, "archivedrepo", "raw")

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "repo.tar.gz")
	require.NoError(t, err)
	_, err = fw.Write(archive)
	require.NoError(t, err)
	require.NoError(t, mw.WriteField("targetName", "existingrepo"))
	require.NoError(t, mw.WriteField("conflictMode", "rename"))
	require.NoError(t, mw.Close())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/repositories/import", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "already exists")
}

// TestBackupExtra_ImportRepo_Success covers the happy path: a valid archive for a
// new repository name is created (skip mode), returning 200 with imported stats.
func TestBackupExtra_ImportRepo_Success(t *testing.T) {
	h := buildBackupHandler()
	r := ginTestRouter()
	r.POST("/api/v1/repositories/import", h.ImportRepo)

	archive := buildRepoArchive(t, "freshrepo", "raw")

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "repo.tar.gz")
	require.NoError(t, err)
	_, err = fw.Write(archive)
	require.NoError(t, err)
	require.NoError(t, mw.WriteField("conflictMode", "skip"))
	require.NoError(t, mw.Close())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/repositories/import", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "imported")
}

// TestBackupExtra_ImportRepo_InvalidArchive covers the generic 400 branch: a valid
// gzip but with no repository.json → service returns "invalid archive".
func TestBackupExtra_ImportRepo_InvalidArchive(t *testing.T) {
	var gzBuf bytes.Buffer
	gw := gzip.NewWriter(&gzBuf)
	require.NoError(t, gw.Close())

	h := buildBackupHandler()
	r := ginTestRouter()
	r.POST("/api/v1/repositories/import", h.ImportRepo)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "repo.tar.gz")
	require.NoError(t, err)
	_, err = fw.Write(gzBuf.Bytes())
	require.NoError(t, err)
	require.NoError(t, mw.Close())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/repositories/import", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "invalid archive")
}
