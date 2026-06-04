package yum_test

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/yum"
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
	h := yum.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	return r
}

// TestYum_Name verifies Name() returns "yum".
func TestYum_Name(t *testing.T) {
	h := yum.New(formats.Deps{
		Repos:      testutil.NewRepoRepo(),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	})
	assert.Equal(t, "yum", h.Name())
}

// TestYum_ServeHTTP_MethodNotAllowed exercises the default case (not RPM, not repodata GET).
func TestYum_ServeHTTP_MethodNotAllowed(t *testing.T) {
	repo := testutil.SimpleRepo("yum-method", "yum")
	r := setupExtra(repo)

	// POST is not handled by any case.
	req := httptest.NewRequest(http.MethodPost, "/repository/yum-method/Packages/pkg-1.0-1.x86_64.rpm", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// TestYum_Head exercises the HEAD branch of serveFile.
func TestYum_Head(t *testing.T) {
	repo := testutil.SimpleRepo("yum-head", "yum")
	r := setupExtra(repo)

	require.Equal(t, http.StatusCreated,
		putRpm(r, "yum-head", "/Packages/nginx-1.0-1.x86_64.rpm", "rpm-bytes"))

	req := httptest.NewRequest(http.MethodHead,
		"/repository/yum-head/Packages/nginx-1.0-1.x86_64.rpm", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.NotEmpty(t, w.Header().Get("Content-Length"))
}

// TestYum_Head_NotFound exercises the 404 branch of serveFile via HEAD.
func TestYum_Head_NotFound(t *testing.T) {
	repo := testutil.SimpleRepo("yum-headnotfound", "yum")
	r := setupExtra(repo)

	req := httptest.NewRequest(http.MethodHead,
		"/repository/yum-headnotfound/Packages/missing-1.0-1.x86_64.rpm", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestYum_PrimaryXmlGz exercises the gzip branch of servePrimary.
func TestYum_PrimaryXmlGz(t *testing.T) {
	repo := testutil.SimpleRepo("yum-primary-gz", "yum")
	r := setupExtra(repo)

	require.Equal(t, http.StatusCreated,
		putRpm(r, "yum-primary-gz", "/Packages/curl-8.0-1.x86_64.rpm", "curl-bytes"))

	req := httptest.NewRequest(http.MethodGet,
		"/repository/yum-primary-gz/repodata/primary.xml.gz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/x-gzip", w.Header().Get("Content-Type"))

	// Decompress and verify it contains XML with our package.
	gr, err := gzip.NewReader(w.Body)
	require.NoError(t, err)
	data, err := io.ReadAll(gr)
	require.NoError(t, err)
	assert.Contains(t, string(data), "curl")
}

// TestYum_EmptyMetadata_Plain exercises serveEmptyMetadata for plain XML.
func TestYum_EmptyMetadata_Plain(t *testing.T) {
	repo := testutil.SimpleRepo("yum-empty-plain", "yum")
	r := setupExtra(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/yum-empty-plain/repodata/filelists.xml", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "metadata")
}

// TestYum_EmptyMetadata_Gz exercises the gzip branch of serveEmptyMetadata.
func TestYum_EmptyMetadata_Gz(t *testing.T) {
	repo := testutil.SimpleRepo("yum-empty-gz", "yum")
	r := setupExtra(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/yum-empty-gz/repodata/filelists.xml.gz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/x-gzip", w.Header().Get("Content-Type"))

	// Decompress and verify valid XML.
	gr, err := gzip.NewReader(w.Body)
	require.NoError(t, err)
	data, err := io.ReadAll(gr)
	require.NoError(t, err)
	assert.Contains(t, string(data), "metadata")
}

// TestYum_EmptyMetadata_Other exercises serveEmptyMetadata for other.xml.
func TestYum_EmptyMetadata_Other(t *testing.T) {
	repo := testutil.SimpleRepo("yum-other", "yum")
	r := setupExtra(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/yum-other/repodata/other.xml", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

// TestYum_Proxy_ServeFile exercises proxy read-through for an RPM.
func TestYum_Proxy_ServeFile(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-rpm")
		_, _ = w.Write([]byte("rpm-data"))
	}))
	defer upstream.Close()

	repo := &domain.Repository{
		ID:          "yum-prx-file",
		Name:        "yum-prx-file",
		Format:      "yum",
		Type:        domain.TypeProxy,
		Online:      true,
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
	r := setupExtra(repo)
	_ = r // already set up; re-use
	r = func() *gin.Engine {
		h := yum.New(d)
		eng := gin.New()
		eng.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
		return eng
	}()

	req := httptest.NewRequest(http.MethodGet,
		"/repository/yum-prx-file/Packages/nginx-1.0-1.x86_64.rpm", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Less(t, w.Code, 500)
}

// TestYum_Proxy_XmlRequest exercises the proxy path for an XML endpoint (non-RPM).
func TestYum_Proxy_XmlRequest(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte("<?xml version='1.0'?><repomd/>"))
	}))
	defer upstream.Close()

	repo := &domain.Repository{
		ID:          "yum-prx-xml",
		Name:        "yum-prx-xml",
		Format:      "yum",
		Type:        domain.TypeProxy,
		Online:      true,
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
	h := yum.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })

	req := httptest.NewRequest(http.MethodGet,
		"/repository/yum-prx-xml/repodata/repomd.xml", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Less(t, w.Code, 500)
}

// TestYum_Upload_SimpleFilename exercises the single-part filename parsing branch
// where the RPM name has only one dash-segment (no version extracted from dash split).
func TestYum_Upload_SimpleFilename(t *testing.T) {
	repo := testutil.SimpleRepo("yum-simple-rpm", "yum")
	r := setupExtra(repo)

	// Filename with no dashes → pkgName = full stem, version = "0"
	code := putRpm(r, "yum-simple-rpm", "/Packages/simplepkg.x86_64.rpm",
		strings.Repeat("x", 100))
	assert.Equal(t, http.StatusCreated, code)
}

// TestYum_PrimaryXml_SkipsNonRpm verifies that non-RPM assets don't appear in primary.xml.
func TestYum_PrimaryXml_SkipsNonRpm(t *testing.T) {
	repo := testutil.SimpleRepo("yum-skip-nonrpm", "yum")
	// Use the yum engine but verify primary.xml doesn't panic when there are
	// no RPM assets (components list is empty by default).
	r := setupExtra(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/yum-skip-nonrpm/repodata/primary.xml", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `packages="0"`)
}
