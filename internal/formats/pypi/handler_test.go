package pypi_test

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/pypi"
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
	h := pypi.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	return r
}

// buildUpload creates a multipart body for twine-style upload.
func buildUpload(pkgName, version, filename, content string) (*bytes.Buffer, string) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField(":action", "file_upload")
	_ = w.WriteField("name", pkgName)
	_ = w.WriteField("version", version)
	part, _ := w.CreateFormFile("content", filename)
	_, _ = part.Write([]byte(content))
	w.Close()
	return &buf, w.FormDataContentType()
}

func upload(r *gin.Engine, repoName, pkgName, version, filename, content string) int {
	body, ct := buildUpload(pkgName, version, filename, content)
	req := httptest.NewRequest(http.MethodPost, "/repository/"+repoName+"/", body)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

func TestPyPI_Upload(t *testing.T) {
	repo := testutil.SimpleRepo("pypi-hosted", "pypi")
	r := setup(repo)

	code := upload(r, "pypi-hosted", "mypackage", "1.0.0", "mypackage-1.0.0-py3-none-any.whl", "wheel-bytes")
	assert.Equal(t, http.StatusOK, code)
}

func TestPyPI_UploadAndDownload(t *testing.T) {
	repo := testutil.SimpleRepo("pypi-dl", "pypi")
	r := setup(repo)

	content := "wheel file content"
	require.Equal(t, http.StatusOK,
		upload(r, "pypi-dl", "mylib", "2.0.0", "mylib-2.0.0-py3-none-any.whl", content))

	req := httptest.NewRequest(http.MethodGet,
		"/repository/pypi-dl/packages/mylib/mylib-2.0.0-py3-none-any.whl", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, content, w.Body.String())
}

func TestPyPI_SimpleIndex_Empty(t *testing.T) {
	repo := testutil.SimpleRepo("pypi-idx", "pypi")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet, "/repository/pypi-idx/simple/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Simple Index")
}

func TestPyPI_SimpleIndex_ShowsPackage(t *testing.T) {
	repo := testutil.SimpleRepo("pypi-idx2", "pypi")
	r := setup(repo)

	require.Equal(t, http.StatusOK,
		upload(r, "pypi-idx2", "requests", "2.31.0", "requests-2.31.0-py3-none-any.whl", "x"))

	req := httptest.NewRequest(http.MethodGet, "/repository/pypi-idx2/simple/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "requests")
}

func TestPyPI_PackageIndex(t *testing.T) {
	repo := testutil.SimpleRepo("pypi-pkg", "pypi")
	r := setup(repo)

	require.Equal(t, http.StatusOK,
		upload(r, "pypi-pkg", "flask", "3.0.0", "flask-3.0.0-py3-none-any.whl", "bytes"))

	req := httptest.NewRequest(http.MethodGet, "/repository/pypi-pkg/simple/flask/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "flask")
}

func TestPyPI_Upload_MissingContentFile_Returns400(t *testing.T) {
	repo := testutil.SimpleRepo("pypi-bad", "pypi")
	r := setup(repo)

	// POST with no multipart content
	req := httptest.NewRequest(http.MethodPost, "/repository/pypi-bad/", nil)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=xxx")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPyPI_ProxyPOST_Rejected(t *testing.T) {
	repo := &domain.Repository{
		ID: "p", Name: "pypi-proxy", Format: "pypi",
		Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": "https://pypi.org"},
	}
	r := setup(repo)

	body, ct := buildUpload("pkg", "1.0", "pkg-1.0.tar.gz", "x")
	req := httptest.NewRequest(http.MethodPost, "/repository/pypi-proxy/", body)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}
