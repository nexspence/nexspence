package gomod_test

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
	"github.com/nexspence-oss/nexspence/internal/formats/gomod"
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
	h := gomod.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	return r
}

func putModule(r *gin.Engine, repoName, path, content string) int {
	req := httptest.NewRequest(http.MethodPut, "/repository/"+repoName+path,
		strings.NewReader(content))
	req.ContentLength = int64(len(content))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

func TestGoMod_PutAndGetZip(t *testing.T) {
	repo := testutil.SimpleRepo("mods", "go")
	r := setup(repo)

	code := putModule(r, "mods", "/github.com/example/lib/@v/v1.0.0.zip", "zip-content")
	require.Equal(t, http.StatusCreated, code)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/mods/github.com/example/lib/@v/v1.0.0.zip", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "zip-content", w.Body.String())
}

func TestGoMod_PutAndGetMod(t *testing.T) {
	repo := testutil.SimpleRepo("mods2", "go")
	r := setup(repo)

	modContent := "module github.com/example/lib\n\ngo 1.21\n"
	code := putModule(r, "mods2", "/github.com/example/lib/@v/v1.0.0.mod", modContent)
	require.Equal(t, http.StatusCreated, code)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/mods2/github.com/example/lib/@v/v1.0.0.mod", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, modContent, w.Body.String())
}

func TestGoMod_List_Empty(t *testing.T) {
	repo := testutil.SimpleRepo("mods3", "go")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/mods3/github.com/example/lib/@v/list", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "", w.Body.String())
}

func TestGoMod_List_AfterUpload(t *testing.T) {
	repo := testutil.SimpleRepo("mods4", "go")
	r := setup(repo)

	require.Equal(t, http.StatusCreated,
		putModule(r, "mods4", "/github.com/foo/bar/@v/v2.0.0.zip", "zip-v2"))

	req := httptest.NewRequest(http.MethodGet,
		"/repository/mods4/github.com/foo/bar/@v/list", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "v2.0.0")
}

func TestGoMod_Info(t *testing.T) {
	repo := testutil.SimpleRepo("mods5", "go")
	r := setup(repo)

	require.Equal(t, http.StatusCreated,
		putModule(r, "mods5", "/example.com/pkg/@v/v0.5.0.zip", "zip-content"))

	req := httptest.NewRequest(http.MethodGet,
		"/repository/mods5/example.com/pkg/@v/v0.5.0.info", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "v0.5.0")
	assert.Contains(t, w.Body.String(), "Version")
}

func TestGoMod_Info_NotFound(t *testing.T) {
	repo := testutil.SimpleRepo("mods6", "go")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/mods6/example.com/pkg/@v/v9.9.9.info", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGoMod_Latest(t *testing.T) {
	repo := testutil.SimpleRepo("mods7", "go")
	r := setup(repo)

	require.Equal(t, http.StatusCreated,
		putModule(r, "mods7", "/example.com/lib/@v/v1.2.3.zip", "zip"))

	req := httptest.NewRequest(http.MethodGet,
		"/repository/mods7/example.com/lib/@latest", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Version")
}

func TestGoMod_ProxyRejectMutation(t *testing.T) {
	repo := testutil.SimpleRepo("mods8", "go")
	repo.Type = domain.TypeProxy

	r := setup(repo)
	code := putModule(r, "mods8", "/example.com/lib/@v/v1.0.0.zip", "data")
	assert.Equal(t, http.StatusMethodNotAllowed, code)
}
