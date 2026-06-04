package pypi_test

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/pypi"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func setupExtra(repo *domain.Repository) *gin.Engine {
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

// TestPyPI_Name verifies Name() returns "pypi".
func TestPyPI_Name(t *testing.T) {
	h := pypi.New(formats.Deps{
		Repos:      testutil.NewRepoRepo(),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	})
	assert.Equal(t, "pypi", h.Name())
}

// TestPyPI_ServeHTTP_MethodNotAllowed exercises the default case in ServeHTTP.
func TestPyPI_ServeHTTP_MethodNotAllowed(t *testing.T) {
	repo := testutil.SimpleRepo("pypi-method", "pypi")
	r := setupExtra(repo)

	req := httptest.NewRequest(http.MethodDelete, "/repository/pypi-method/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// TestPyPI_Upload_UnsupportedAction exercises the bad :action branch.
func TestPyPI_Upload_UnsupportedAction(t *testing.T) {
	repo := testutil.SimpleRepo("pypi-action", "pypi")
	r := setupExtra(repo)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField(":action", "other_action")
	_ = mw.WriteField("name", "mypkg")
	_ = mw.WriteField("version", "1.0.0")
	part, _ := mw.CreateFormFile("content", "mypkg-1.0.0.whl")
	_, _ = part.Write([]byte("x"))
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/repository/pypi-action/", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestPyPI_Upload_MissingNameOrVersion exercises the name/version validation branch.
func TestPyPI_Upload_MissingNameOrVersion(t *testing.T) {
	repo := testutil.SimpleRepo("pypi-noname", "pypi")
	r := setupExtra(repo)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField(":action", "file_upload")
	// intentionally skip "name" and "version"
	part, _ := mw.CreateFormFile("content", "mypkg-1.0.0.whl")
	_, _ = part.Write([]byte("x"))
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/repository/pypi-noname/", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestPyPI_Upload_ContentTypeFromFilename exercises the pypiContentType fallback
// when the file part has no Content-Type header (covers all extension branches).
func TestPyPI_Upload_ContentTypeFromFilename(t *testing.T) {
	cases := []struct {
		filename string
	}{
		{"mypkg-1.0.tar.gz"},
		{"mypkg-1.0.tgz"},
		{"mypkg-1.0.zip"},
		{"mypkg-1.0.whl"},
		{"mypkg-1.0.egg"}, // falls into default octet-stream
	}
	for _, tc := range cases {
		t.Run(tc.filename, func(t *testing.T) {
			repoName := "pypi-ct-" + strings.ReplaceAll(tc.filename, ".", "-")
			repo := testutil.SimpleRepo(repoName, "pypi")
			r := setupExtra(repo)

			body, ct := buildUpload("mypkg", "1.0", tc.filename, "data")
			req := httptest.NewRequest(http.MethodPost, "/repository/"+repoName+"/", body)
			req.Header.Set("Content-Type", ct)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			assert.Equal(t, http.StatusOK, w.Code, "filename: %s", tc.filename)
		})
	}
}

// TestPyPI_ServeFile_Head exercises the HEAD branch of serveFile.
func TestPyPI_ServeFile_Head(t *testing.T) {
	repo := testutil.SimpleRepo("pypi-head", "pypi")
	r := setupExtra(repo)

	// Upload first.
	require.Equal(t, http.StatusOK,
		upload(r, "pypi-head", "headpkg", "1.0.0", "headpkg-1.0.0-py3-none-any.whl", "content"))

	// HEAD request.
	req := httptest.NewRequest(http.MethodHead,
		"/repository/pypi-head/packages/headpkg/headpkg-1.0.0-py3-none-any.whl", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.NotEmpty(t, w.Header().Get("Content-Length"))
}

// TestPyPI_ServeFile_NotFound exercises the 404 branch of serveFile.
func TestPyPI_ServeFile_NotFound(t *testing.T) {
	repo := testutil.SimpleRepo("pypi-404", "pypi")
	r := setupExtra(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/pypi-404/packages/nope/nope-1.0.0.whl", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestPyPI_SimpleIndex_Path covers the /simple (no trailing slash) variant.
func TestPyPI_SimpleIndex_NormPath(t *testing.T) {
	repo := testutil.SimpleRepo("pypi-simple-norm", "pypi")
	r := setupExtra(repo)

	// path.Clean strips the trailing slash so both /simple and /simple/ reach the same branch
	req := httptest.NewRequest(http.MethodGet, "/repository/pypi-simple-norm/simple", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Simple Index")
}

// TestPyPI_PackageIndex_NoPkgFound exercises servePackageIndex for an unknown package.
func TestPyPI_PackageIndex_NoPkgFound(t *testing.T) {
	repo := testutil.SimpleRepo("pypi-pkg-empty", "pypi")
	r := setupExtra(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/pypi-pkg-empty/simple/nonexistent/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "nonexistent")
}

// TestPyPI_Proxy_SimpleIndex exercises the proxy branch for /simple/ (ServeGET path).
func TestPyPI_Proxy_SimpleIndex(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "<!DOCTYPE html><html><body><a href='/requests/'>requests</a></body></html>")
	}))
	defer upstream.Close()

	repo := &domain.Repository{
		ID:          "pypi-prx-simple",
		Name:        "pypi-prx-simple",
		Format:      "pypi",
		Type:        domain.TypeProxy,
		Online:      true,
		ProxyConfig: map[string]any{"remote_url": upstream.URL},
	}
	r := setupExtra(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/pypi-prx-simple/simple/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	// repoproxy serves it; any non-5xx is fine
	assert.Less(t, w.Code, 500)
}

// TestPyPI_Proxy_PackageIndex exercises the proxy branch for /simple/:name/.
func TestPyPI_Proxy_PackageIndex(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "<!DOCTYPE html><html><body><a href='requests-2.31.0.whl'>file</a></body></html>")
	}))
	defer upstream.Close()

	repo := &domain.Repository{
		ID:          "pypi-prx-pkg",
		Name:        "pypi-prx-pkg",
		Format:      "pypi",
		Type:        domain.TypeProxy,
		Online:      true,
		ProxyConfig: map[string]any{"remote_url": upstream.URL},
	}
	r := setupExtra(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/pypi-prx-pkg/simple/requests/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Less(t, w.Code, 500)
}

// TestPyPI_Proxy_ServeFile exercises the proxy branch of serveFile.
func TestPyPI_Proxy_ServeFile(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write([]byte("wheel-bytes"))
	}))
	defer upstream.Close()

	repo := &domain.Repository{
		ID:          "pypi-prx-file",
		Name:        "pypi-prx-file",
		Format:      "pypi",
		Type:        domain.TypeProxy,
		Online:      true,
		ProxyConfig: map[string]any{"remote_url": upstream.URL},
	}
	r := setupExtra(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/pypi-prx-file/packages/requests/requests-2.31.0-py3-none-any.whl", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Less(t, w.Code, 500)
}

// TestPyPI_Upload_WithoutActionField exercises the empty action (allowed) branch.
func TestPyPI_Upload_WithoutActionField(t *testing.T) {
	repo := testutil.SimpleRepo("pypi-noaction", "pypi")
	r := setupExtra(repo)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	// No :action field — empty string is treated as "file_upload" equivalent
	_ = mw.WriteField("name", "mypkg")
	_ = mw.WriteField("version", "1.0.0")
	part, _ := mw.CreateFormFile("content", "mypkg-1.0.0-py3-none-any.whl")
	_, _ = part.Write([]byte("wheel-data"))
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/repository/pypi-noaction/", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

// TestPyPI_Proxy_ServeFile_ContentTypes exercises pypiContentType via the proxy serveFile path
// for tar.gz, tgz, zip, and the default (unknown extension) branches.
func TestPyPI_Proxy_ServeFile_ContentTypes(t *testing.T) {
	filenames := []string{
		"pkg-1.0.tar.gz",
		"pkg-1.0.tgz",
		"pkg-1.0.zip",
		"pkg-1.0.egg", // default → application/octet-stream
	}
	for _, fn := range filenames {
		t.Run(fn, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte("file-bytes"))
			}))
			defer upstream.Close()

			repo := &domain.Repository{
				ID:          "pypi-prx-ct-" + fn,
				Name:        "pypi-prx-ct-" + fn,
				Format:      "pypi",
				Type:        domain.TypeProxy,
				Online:      true,
				ProxyConfig: map[string]any{"remote_url": upstream.URL},
			}
			r := setupExtra(repo)

			req := httptest.NewRequest(http.MethodGet,
				"/repository/pypi-prx-ct-"+fn+"/packages/pkg/"+fn, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			assert.Less(t, w.Code, 500, "file: %s", fn)
		})
	}
}

// TestPyPI_ServeFile_ChecksumHeader verifies the X-Checksum-SHA256 header is set.
func TestPyPI_ServeFile_ChecksumHeader(t *testing.T) {
	repo := testutil.SimpleRepo("pypi-cksum", "pypi")
	r := setupExtra(repo)

	require.Equal(t, http.StatusOK,
		upload(r, "pypi-cksum", "ckpkg", "1.0.0", "ckpkg-1.0.0-py3-none-any.whl", "wheel-content"))

	req := httptest.NewRequest(http.MethodGet,
		"/repository/pypi-cksum/packages/ckpkg/ckpkg-1.0.0-py3-none-any.whl", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	// SHA256 is computed by base.StoreArtifact and stored, so the header should be present.
	assert.NotEmpty(t, w.Header().Get("X-Checksum-SHA256"))
}
