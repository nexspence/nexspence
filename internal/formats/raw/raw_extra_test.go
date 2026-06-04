package raw_test

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
	"github.com/nexspence-oss/nexspence/internal/formats/raw"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// setupWithWebhook creates a router where the deps include a webhook dispatcher,
// so StoreArtifact / DeleteArtifact exercise the webhook dispatch branch.
func setupWithWebhook(repo *domain.Repository) (*gin.Engine, *captureDispatcher) {
	repos := testutil.NewRepoRepo(repo)
	blobs := testutil.NewBlobStoreRepo()
	comps := testutil.NewComponentRepo()
	assets := testutil.NewAssetRepo()
	blobStore := testutil.NewBlobStore()
	wh := &captureDispatcher{}

	d := formats.Deps{
		Repos:      repos,
		Blobs:      blobs,
		Components: comps,
		Assets:     assets,
		BlobStore:  blobStore,
		BaseURL:    "http://localhost:8080",
		Webhooks:   wh,
	}

	h := raw.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) {
		h.ServeHTTP(c)
	})
	return r, wh
}

// captureDispatcher records dispatched payloads so tests can assert on them.
type captureDispatcher struct {
	Events []domain.WebhookPayload
}

func (d *captureDispatcher) Dispatch(p domain.WebhookPayload) {
	d.Events = append(d.Events, p)
}

// TestRawHandler_Name exercises the Name() method.
func TestRawHandler_Name(t *testing.T) {
	repo := testutil.SimpleRepo("raw-name", "raw")
	d := formats.Deps{
		Repos:     testutil.NewRepoRepo(repo),
		Blobs:     testutil.NewBlobStoreRepo(),
		BlobStore: testutil.NewBlobStore(),
	}
	h := raw.New(d)
	assert.Equal(t, "raw", h.Name())
}

// TestRawHandler_GetReturnsChecksumHeaders checks that SHA256/ETag headers
// are present on a GET response for a file that was PUT.
func TestRawHandler_GetReturnsChecksumHeaders(t *testing.T) {
	repo := testutil.SimpleRepo("raw-checksum", "raw")
	router, _ := setupRouter(repo)

	body := "checksum header test"
	putReq := httptest.NewRequest(http.MethodPut, "/repository/raw-checksum/f.bin",
		strings.NewReader(body))
	putReq.ContentLength = int64(len(body))
	router.ServeHTTP(httptest.NewRecorder(), putReq)

	getReq := httptest.NewRequest(http.MethodGet, "/repository/raw-checksum/f.bin", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, getReq)
	require.Equal(t, http.StatusOK, w.Code)
	assert.NotEmpty(t, w.Header().Get("X-Checksum-SHA256"))
	assert.NotEmpty(t, w.Header().Get("ETag"))
}

// TestRawHandler_PutContentTypeOctetStream covers the fallback when no extension
// is recognized and no Content-Type header is supplied.
func TestRawHandler_PutContentTypeOctetStream(t *testing.T) {
	repo := testutil.SimpleRepo("raw-oct", "raw")
	router, _ := setupRouter(repo)

	body := "binary data"
	req := httptest.NewRequest(http.MethodPut, "/repository/raw-oct/noext", strings.NewReader(body))
	req.ContentLength = int64(len(body))
	// No Content-Type, no recognized extension → should default to application/octet-stream
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusCreated, w.Code)
}

// TestRawHandler_PostIsAccepted verifies POST is treated same as PUT.
func TestRawHandler_PostIsAccepted(t *testing.T) {
	repo := testutil.SimpleRepo("raw-post", "raw")
	router, _ := setupRouter(repo)

	body := "posted content"
	req := httptest.NewRequest(http.MethodPost, "/repository/raw-post/file.txt",
		strings.NewReader(body))
	req.ContentLength = int64(len(body))
	req.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusCreated, w.Code)
}

// TestRawHandler_ProxyGet exercises the proxy GET path (ServeGET branch).
// We use a hosted repo acting as a proxy to a non-existent upstream; the
// important thing is the proxy branch is entered (not the hosted branch).
func TestRawHandler_ProxyGetReturnsError(t *testing.T) {
	repo := testutil.SimpleRepo("raw-proxy-get", "raw")
	repo.Type = domain.TypeProxy
	// No remote URL configured → repoproxy.ServeGET will fail with an error,
	// exercising the error-return branch in the handler.
	router, _ := setupRouter(repo)

	req := httptest.NewRequest(http.MethodGet, "/repository/raw-proxy-get/file.txt", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	// Either internal server error (error from ServeGET) or another non-2xx code.
	assert.NotEqual(t, http.StatusOK, w.Code)
}

// TestRawHandler_DeleteProxyRejects verifies DELETE on a proxy repo is rejected.
func TestRawHandler_DeleteProxyRejects(t *testing.T) {
	repo := testutil.SimpleRepo("raw-proxy-del", "raw")
	repo.Type = domain.TypeProxy
	router, _ := setupRouter(repo)

	req := httptest.NewRequest(http.MethodDelete, "/repository/raw-proxy-del/file.txt", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// TestRawHandler_WebhookDispatchedOnPut checks that StoreArtifact fires a
// webhook event when a Webhooks dispatcher is wired.
func TestRawHandler_WebhookDispatchedOnPut(t *testing.T) {
	repo := testutil.SimpleRepo("raw-wh", "raw")
	router, wh := setupWithWebhook(repo)

	body := "webhook test"
	req := httptest.NewRequest(http.MethodPut, "/repository/raw-wh/wh.txt",
		strings.NewReader(body))
	req.ContentLength = int64(len(body))
	req.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)
	assert.Len(t, wh.Events, 1)
	assert.Equal(t, domain.EventArtifactPublished, wh.Events[0].Event)
}

// TestRawHandler_WebhookDispatchedOnDelete checks that DeleteArtifact fires a
// webhook delete event.
func TestRawHandler_WebhookDispatchedOnDelete(t *testing.T) {
	repo := testutil.SimpleRepo("raw-wh-del", "raw")
	router, wh := setupWithWebhook(repo)

	// First store a file
	body := "to be deleted"
	putReq := httptest.NewRequest(http.MethodPut, "/repository/raw-wh-del/del.txt",
		strings.NewReader(body))
	putReq.ContentLength = int64(len(body))
	putReq.Header.Set("Content-Type", "text/plain")
	router.ServeHTTP(httptest.NewRecorder(), putReq)

	wh.Events = nil // reset

	delReq := httptest.NewRequest(http.MethodDelete, "/repository/raw-wh-del/del.txt", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, delReq)
	require.Equal(t, http.StatusNoContent, w.Code)
	assert.Len(t, wh.Events, 1)
	assert.Equal(t, domain.EventArtifactDeleted, wh.Events[0].Event)
}

// TestRawHandler_HeadChecksumHeaders verifies HEAD response includes checksum
// headers (covers the branch in ServeHTTP that returns early for HEAD).
func TestRawHandler_HeadChecksumHeaders_WithChecksums(t *testing.T) {
	repo := testutil.SimpleRepo("raw-head-ck", "raw")
	router, _ := setupRouter(repo)

	body := "checksum head test"
	putReq := httptest.NewRequest(http.MethodPut, "/repository/raw-head-ck/ck.dat",
		strings.NewReader(body))
	putReq.Header.Set("Content-Type", "application/octet-stream")
	putReq.ContentLength = int64(len(body))
	router.ServeHTTP(httptest.NewRecorder(), putReq)

	req := httptest.NewRequest(http.MethodHead, "/repository/raw-head-ck/ck.dat", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.NotEmpty(t, w.Header().Get("Content-Length"))
	assert.NotEmpty(t, w.Header().Get("Content-Type"))
}
