package apt_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/apt"
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
	h := apt.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	return r
}

func putDeb(r *gin.Engine, repoName, path, content string) int {
	req := httptest.NewRequest(http.MethodPut, "/repository/"+repoName+path,
		strings.NewReader(content))
	req.ContentLength = int64(len(content))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

func TestApt_UploadAndDownload(t *testing.T) {
	repo := testutil.SimpleRepo("debs", "apt")
	r := setup(repo)

	require.Equal(t, http.StatusCreated,
		putDeb(r, "debs", "/pool/main/nginx_1.25.0_amd64.deb", "deb-bytes"))

	req := httptest.NewRequest(http.MethodGet,
		"/repository/debs/pool/main/nginx_1.25.0_amd64.deb", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "deb-bytes", w.Body.String())
}

func TestApt_Release(t *testing.T) {
	repo := testutil.SimpleRepo("debs2", "apt")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/debs2/dists/focal/Release", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "focal")
	assert.Contains(t, w.Body.String(), "Nexspence")
}

func TestApt_PackagesIndex_Empty(t *testing.T) {
	repo := testutil.SimpleRepo("debs3", "apt")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/debs3/dists/focal/main/binary-amd64/Packages", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "", w.Body.String())
}

func TestApt_PackagesIndex_ShowsPackage(t *testing.T) {
	repo := testutil.SimpleRepo("debs4", "apt")
	r := setup(repo)

	require.Equal(t, http.StatusCreated,
		putDeb(r, "debs4", "/pool/main/curl_8.0.0_amd64.deb", "curl-bytes"))

	req := httptest.NewRequest(http.MethodGet,
		"/repository/debs4/dists/focal/main/binary-amd64/Packages", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "curl")
}

func TestApt_Delete(t *testing.T) {
	repo := testutil.SimpleRepo("debs5", "apt")
	r := setup(repo)

	require.Equal(t, http.StatusCreated,
		putDeb(r, "debs5", "/pool/main/vim_9.0_amd64.deb", "vim-bytes"))

	req := httptest.NewRequest(http.MethodDelete,
		"/repository/debs5/pool/main/vim_9.0_amd64.deb", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestApt_GetNotFound(t *testing.T) {
	repo := testutil.SimpleRepo("debs6", "apt")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/debs6/pool/main/missing_1.0_amd64.deb", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestApt_ProxyRejectMutation(t *testing.T) {
	repo := testutil.SimpleRepo("debs7", "apt")
	repo.Type = domain.TypeProxy

	r := setup(repo)
	code := putDeb(r, "debs7", "/pool/main/pkg_1.0_amd64.deb", "data")
	assert.Equal(t, http.StatusMethodNotAllowed, code)
}
