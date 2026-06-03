package yum_test

import (
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
	h := yum.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	return r
}

func putRpm(r *gin.Engine, repoName, path, content string) int {
	req := httptest.NewRequest(http.MethodPut, "/repository/"+repoName+path,
		strings.NewReader(content))
	req.ContentLength = int64(len(content))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

func TestYum_UploadAndDownload(t *testing.T) {
	repo := testutil.SimpleRepo("rpms", "yum")
	r := setup(repo)

	require.Equal(t, http.StatusCreated,
		putRpm(r, "rpms", "/Packages/nginx-1.25.0-1.x86_64.rpm", "rpm-bytes"))

	req := httptest.NewRequest(http.MethodGet,
		"/repository/rpms/Packages/nginx-1.25.0-1.x86_64.rpm", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "rpm-bytes", w.Body.String())
}

func TestYum_RepomdXml(t *testing.T) {
	repo := testutil.SimpleRepo("rpms2", "yum")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet, "/repository/rpms2/repodata/repomd.xml", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "repomd")
	assert.Contains(t, w.Body.String(), "primary")
}

func TestYum_PrimaryXml_Empty(t *testing.T) {
	repo := testutil.SimpleRepo("rpms3", "yum")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet, "/repository/rpms3/repodata/primary.xml", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "metadata")
}

func TestYum_PrimaryXml_AfterUpload(t *testing.T) {
	repo := testutil.SimpleRepo("rpms4", "yum")
	r := setup(repo)

	require.Equal(t, http.StatusCreated,
		putRpm(r, "rpms4", "/Packages/curl-8.0.0-1.x86_64.rpm", "curl-bytes"))

	req := httptest.NewRequest(http.MethodGet, "/repository/rpms4/repodata/primary.xml", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "curl")
}

func TestYum_Delete(t *testing.T) {
	repo := testutil.SimpleRepo("rpms5", "yum")
	r := setup(repo)

	require.Equal(t, http.StatusCreated,
		putRpm(r, "rpms5", "/Packages/vim-9.0-1.x86_64.rpm", "vim-bytes"))

	req := httptest.NewRequest(http.MethodDelete,
		"/repository/rpms5/Packages/vim-9.0-1.x86_64.rpm", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestYum_GetNotFound(t *testing.T) {
	repo := testutil.SimpleRepo("rpms6", "yum")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/rpms6/Packages/missing-1.0-1.x86_64.rpm", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestYum_ProxyRejectMutation(t *testing.T) {
	repo := testutil.SimpleRepo("rpms7", "yum")
	repo.Type = domain.TypeProxy

	r := setup(repo)
	code := putRpm(r, "rpms7", "/Packages/pkg-1.0-1.x86_64.rpm", "data")
	assert.Equal(t, http.StatusMethodNotAllowed, code)
}
