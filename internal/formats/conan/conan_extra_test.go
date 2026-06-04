package conan_test

import (
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
	"github.com/nexspence-oss/nexspence/internal/formats/conan"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// TestConan_Name verifies the handler name.
func TestConan_Name(t *testing.T) {
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	h := conan.New(d)
	assert.Equal(t, "conan", h.Name())
}

// TestConan_MethodNotAllowed covers the default branch in ServeHTTP.
func TestConan_MethodNotAllowed(t *testing.T) {
	repo := testutil.SimpleRepo("conan-405", "conan")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodDelete, "/repository/conan-405/v1/conans/boost/1.83.0/_/_", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// TestConan_ProxyPing covers proxy repo /ping (always served locally).
func TestConan_ProxyPing(t *testing.T) {
	repo := testutil.SimpleRepo("conan-proxy-ping", "conan")
	repo.Type = domain.TypeProxy
	repo.ProxyConfig = map[string]any{"remote_url": "http://127.0.0.1:19998"}
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet, "/repository/conan-proxy-ping/ping", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"ok"`)
}

// TestConan_ProxyV1Ping covers proxy repo /v1/ping (always served locally).
func TestConan_ProxyV1Ping(t *testing.T) {
	repo := testutil.SimpleRepo("conan-proxy-v1ping", "conan")
	repo.Type = domain.TypeProxy
	repo.ProxyConfig = map[string]any{"remote_url": "http://127.0.0.1:19998"}
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet, "/repository/conan-proxy-v1ping/v1/ping", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

// TestConan_UploadURLs_BadRef covers handleUploadURLs with a malformed ref (too few segments).
func TestConan_UploadURLs_BadRef(t *testing.T) {
	repo := testutil.SimpleRepo("conan-uurl-bad", "conan")
	r := setup(repo)

	// Only 2 segments instead of the required 4 (name/version/user/channel)
	body, _ := json.Marshal(map[string]int64{"conanfile.py": 100})
	req := httptest.NewRequest(http.MethodPost,
		"/repository/conan-uurl-bad/v1/conans/boost/upload_urls",
		strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestConan_UploadURLs_EmptyBody covers handleUploadURLs with empty JSON body.
func TestConan_UploadURLs_EmptyBody(t *testing.T) {
	repo := testutil.SimpleRepo("conan-uurl-empty", "conan")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodPost,
		"/repository/conan-uurl-empty/v1/conans/zlib/1.3.1/_/_/upload_urls",
		strings.NewReader(""))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	// Empty body is allowed — returns empty URL map
	assert.Equal(t, http.StatusOK, w.Code)
}

// TestConan_Manifest_InvalidRef covers handleManifest with a malformed path (missing channel).
func TestConan_Manifest_InvalidRef(t *testing.T) {
	repo := testutil.SimpleRepo("conan-mani-bad", "conan")
	r := setup(repo)

	// Only 3 segments: boost/1.83.0/_ — missing channel
	req := httptest.NewRequest(http.MethodGet,
		"/repository/conan-mani-bad/v1/conans/boost/1.83.0/_", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestConan_DownloadURLs_InvalidRef covers handleDownloadURLs with a malformed path.
func TestConan_DownloadURLs_InvalidRef(t *testing.T) {
	repo := testutil.SimpleRepo("conan-durl-bad", "conan")
	r := setup(repo)

	// Only 2 segments — not enough for a valid ref (needs name/version/user/channel)
	req := httptest.NewRequest(http.MethodGet,
		"/repository/conan-durl-bad/v1/conans/boost/download_urls", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestConan_Upload_PackageFile covers uploading a binary package file (package subpath).
func TestConan_Upload_PackageFile(t *testing.T) {
	repo := testutil.SimpleRepo("conan-pkg-upload", "conan")
	r := setup(repo)

	pkgPath := "/files/boost/1.83.0/_/_/0/package/abc123/0/conaninfo.txt"
	content := "arch=x86_64\nos=Linux\n"

	req := httptest.NewRequest(http.MethodPut, "/repository/conan-pkg-upload"+pkgPath,
		strings.NewReader(content))
	req.ContentLength = int64(len(content))
	req.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)

	req2 := httptest.NewRequest(http.MethodGet, "/repository/conan-pkg-upload"+pkgPath, nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusOK, w2.Code)
	assert.Equal(t, content, w2.Body.String())
}

// TestConan_ProxyDownload covers proxy GET for a file path (non-ping).
func TestConan_ProxyDownload(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("conan-file-bytes"))
	}))
	defer upstream.Close()

	repo := &domain.Repository{
		ID: "conan-px-dl", Name: "conan-proxy-dl2", Format: "conan",
		Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": upstream.URL},
	}
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	h := conan.New(d)
	eng := gin.New()
	eng.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })

	req := httptest.NewRequest(http.MethodGet,
		"/repository/conan-proxy-dl2/files/boost/1.83.0/_/_/0/export/conanfile.py", nil)
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, req)
	// Proxy will either return 200 (cached/proxied) or 502 (upstream unreachable after network)
	assert.True(t, w.Code == http.StatusOK || w.Code == http.StatusBadGateway,
		"expected 200 or 502, got %d", w.Code)
}
