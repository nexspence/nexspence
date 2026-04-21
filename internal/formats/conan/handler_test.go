package conan_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/conan"
	"github.com/nexspence-oss/nexspence/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	h := conan.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	return r
}

func TestConan_Ping(t *testing.T) {
	repo := testutil.SimpleRepo("conanrepo", "conan")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet, "/repository/conanrepo/ping", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"ok"`)
}

func TestConan_V1Ping(t *testing.T) {
	repo := testutil.SimpleRepo("conanrepo2", "conan")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet, "/repository/conanrepo2/v1/ping", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestConan_UploadAndDownloadFile(t *testing.T) {
	repo := testutil.SimpleRepo("conanrepo3", "conan")
	r := setup(repo)

	filePath := "/files/boost/1.83.0/_/_/0/export/conanfile.py"
	content := "from conans import ConanFile\n"

	req := httptest.NewRequest(http.MethodPut, "/repository/conanrepo3"+filePath,
		strings.NewReader(content))
	req.ContentLength = int64(len(content))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)

	req2 := httptest.NewRequest(http.MethodGet, "/repository/conanrepo3"+filePath, nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusOK, w2.Code)
	assert.Equal(t, content, w2.Body.String())
}

func TestConan_UploadURLs(t *testing.T) {
	repo := testutil.SimpleRepo("conanrepo4", "conan")
	r := setup(repo)

	body, _ := json.Marshal(map[string]int64{
		"conanfile.py":     512,
		"conanmanifest.txt": 128,
	})
	req := httptest.NewRequest(http.MethodPost,
		"/repository/conanrepo4/v1/conans/boost/1.83.0/_/_/upload_urls",
		strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "conanfile.py")
	assert.Contains(t, w.Body.String(), "conanmanifest.txt")
}

func TestConan_DownloadURLs(t *testing.T) {
	repo := testutil.SimpleRepo("conanrepo5", "conan")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/conanrepo5/v1/conans/zlib/1.3.1/_/_/download_urls", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "conanfile.py")
}

func TestConan_Manifest(t *testing.T) {
	repo := testutil.SimpleRepo("conanrepo6", "conan")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/conanrepo6/v1/conans/fmt/10.2.1/user/stable", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "fmt")
	assert.Contains(t, w.Body.String(), "10.2.1")
}

func TestConan_ProxyRejectMutation(t *testing.T) {
	repo := testutil.SimpleRepo("conanrepo7", "conan")
	repo.Type = domain.TypeProxy

	r := setup(repo)
	req := httptest.NewRequest(http.MethodPut,
		"/repository/conanrepo7/files/boost/1.83.0/_/_/0/export/conanfile.py",
		strings.NewReader("data"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func TestConan_DownloadFile_NotFound(t *testing.T) {
	repo := testutil.SimpleRepo("conanrepo8", "conan")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/conanrepo8/files/nonexistent/0.0.1/_/_/0/export/conanfile.py", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}
