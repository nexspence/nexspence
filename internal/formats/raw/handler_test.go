package raw_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/formats/raw"
	"github.com/nexspence-oss/nexspence/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() { gin.SetMode(gin.TestMode) }

// setupRouter creates a Gin engine with the raw handler mounted at /repository/:repoName/*path.
func setupRouter(repo *domain.Repository) (*gin.Engine, *testutil.BlobStore) {
	repos := testutil.NewRepoRepo(repo)
	blobs := testutil.NewBlobStoreRepo()
	comps := testutil.NewComponentRepo()
	assets := testutil.NewAssetRepo()
	blobStore := testutil.NewBlobStore()

	d := formats.Deps{
		Repos:      repos,
		Blobs:      blobs,
		Components: comps,
		Assets:     assets,
		BlobStore:  blobStore,
		BaseURL:    "http://localhost:8080",
	}

	h := raw.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) {
		h.ServeHTTP(c)
	})
	return r, blobStore
}

func TestRawHandler_PutThenGet(t *testing.T) {
	repo := testutil.SimpleRepo("raw-test", "raw")
	router, blobStore := setupRouter(repo)

	body := "hello raw artifact"

	// PUT
	req := httptest.NewRequest(http.MethodPut, "/repository/raw-test/path/to/file.txt", strings.NewReader(body))
	req.Header.Set("Content-Type", "text/plain")
	req.ContentLength = int64(len(body))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusCreated, w.Code)

	// Blob should be stored under the deterministic key
	key := base.BlobKey("raw-test", "/path/to/file.txt")
	assert.True(t, blobStore.Has(key))

	// GET
	req2 := httptest.NewRequest(http.MethodGet, "/repository/raw-test/path/to/file.txt", nil)
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusOK, w2.Code)
	assert.Equal(t, body, w2.Body.String())
	assert.Equal(t, "text/plain", w2.Header().Get("Content-Type"))
}

func TestRawHandler_GetNotFound(t *testing.T) {
	repo := testutil.SimpleRepo("raw-empty", "raw")
	router, _ := setupRouter(repo)

	req := httptest.NewRequest(http.MethodGet, "/repository/raw-empty/missing.jar", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestRawHandler_Delete(t *testing.T) {
	repo := testutil.SimpleRepo("raw-del", "raw")
	router, blobStore := setupRouter(repo)

	body := "to be deleted"
	putReq := httptest.NewRequest(http.MethodPut, "/repository/raw-del/file.bin", strings.NewReader(body))
	putReq.ContentLength = int64(len(body))
	router.ServeHTTP(httptest.NewRecorder(), putReq)

	key := base.BlobKey("raw-del", "/file.bin")
	require.True(t, blobStore.Has(key))

	delReq := httptest.NewRequest(http.MethodDelete, "/repository/raw-del/file.bin", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, delReq)
	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.False(t, blobStore.Has(key))
}

func TestRawHandler_HeadReturnsHeaders(t *testing.T) {
	repo := testutil.SimpleRepo("raw-head", "raw")
	router, _ := setupRouter(repo)

	body := "head test data"
	putReq := httptest.NewRequest(http.MethodPut, "/repository/raw-head/info.dat", strings.NewReader(body))
	putReq.Header.Set("Content-Type", "application/octet-stream")
	putReq.ContentLength = int64(len(body))
	router.ServeHTTP(httptest.NewRecorder(), putReq)

	req := httptest.NewRequest(http.MethodHead, "/repository/raw-head/info.dat", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, w.Body.String(), "HEAD must return empty body")
	assert.NotEmpty(t, w.Header().Get("Content-Length"))
}

func TestRawHandler_MethodNotAllowed(t *testing.T) {
	repo := testutil.SimpleRepo("raw-mna", "raw")
	router, _ := setupRouter(repo)

	req := httptest.NewRequest(http.MethodPatch, "/repository/raw-mna/file.txt", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func TestRawHandler_ProxyRepoRejectsMutation(t *testing.T) {
	repo := testutil.SimpleRepo("raw-proxy", "raw")
	repo.Type = domain.TypeProxy
	router, _ := setupRouter(repo)

	req := httptest.NewRequest(http.MethodPut, "/repository/raw-proxy/file.txt", strings.NewReader("data"))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func TestRawHandler_ContentTypeInferredFromExtension(t *testing.T) {
	repo := testutil.SimpleRepo("raw-ct", "raw")
	router, _ := setupRouter(repo)

	// Upload without explicit Content-Type — handler should infer from .json extension
	body := `{"key":"value"}`
	putReq := httptest.NewRequest(http.MethodPut, "/repository/raw-ct/data.json", strings.NewReader(body))
	putReq.ContentLength = int64(len(body))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, putReq)
	assert.Equal(t, http.StatusCreated, w.Code)

	// GET should return with inferred or stored content type
	getReq := httptest.NewRequest(http.MethodGet, "/repository/raw-ct/data.json", nil)
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, getReq)
	assert.Equal(t, http.StatusOK, w2.Code)
}
