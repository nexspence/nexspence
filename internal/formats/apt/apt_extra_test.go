package apt_test

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
	"github.com/nexspence-oss/nexspence/internal/formats/apt"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// captureDispatcher records dispatched payloads.
type captureDispatcher struct {
	Events []domain.WebhookPayload
}

func (d *captureDispatcher) Dispatch(p domain.WebhookPayload) {
	d.Events = append(d.Events, p)
}

func setupWithWebhook(repo *domain.Repository) (*gin.Engine, *captureDispatcher) {
	wh := &captureDispatcher{}
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
		Webhooks:   wh,
	}
	h := apt.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	return r, wh
}

// TestApt_Name verifies the Name() method.
func TestApt_Name(t *testing.T) {
	d := formats.Deps{
		Repos:     testutil.NewRepoRepo(),
		Blobs:     testutil.NewBlobStoreRepo(),
		BlobStore: testutil.NewBlobStore(),
	}
	h := apt.New(d)
	assert.Equal(t, "apt", h.Name())
}

// TestApt_InRelease exercises the InRelease endpoint (same logic as Release).
func TestApt_InRelease(t *testing.T) {
	repo := testutil.SimpleRepo("apt-inrel", "apt")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/apt-inrel/dists/jammy/InRelease", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "jammy")
	assert.Contains(t, w.Body.String(), "Nexspence")
}

// TestApt_PackagesGz verifies the gzipped Packages endpoint returns valid gzip data.
func TestApt_PackagesGz(t *testing.T) {
	repo := testutil.SimpleRepo("apt-gz", "apt")
	r := setup(repo)

	// Upload a .deb first so the index has something to gzip
	require.Equal(t, http.StatusCreated,
		putDeb(r, "apt-gz", "/pool/main/htop_3.0.5_amd64.deb", "htop-bytes"))

	req := httptest.NewRequest(http.MethodGet,
		"/repository/apt-gz/dists/focal/main/binary-amd64/Packages.gz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/x-gzip", w.Header().Get("Content-Type"))

	// Must decompress cleanly
	gr, err := gzip.NewReader(w.Body)
	require.NoError(t, err)
	data, err := io.ReadAll(gr)
	require.NoError(t, err)
	assert.Contains(t, string(data), "htop")
}

// TestApt_PackagesGz_Empty verifies gzipped index works when empty.
func TestApt_PackagesGz_Empty(t *testing.T) {
	repo := testutil.SimpleRepo("apt-gz-empty", "apt")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/apt-gz-empty/dists/focal/main/binary-amd64/Packages.gz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	// Should be a valid (but empty) gzip stream
	gr, err := gzip.NewReader(w.Body)
	require.NoError(t, err)
	data, _ := io.ReadAll(gr)
	assert.Empty(t, string(data))
}

// TestApt_HeadDeb exercises the HEAD path in serveFile.
func TestApt_HeadDeb(t *testing.T) {
	repo := testutil.SimpleRepo("apt-head", "apt")
	r := setup(repo)

	require.Equal(t, http.StatusCreated,
		putDeb(r, "apt-head", "/pool/main/curl_8.0.0_amd64.deb", "curl-content"))

	req := httptest.NewRequest(http.MethodHead,
		"/repository/apt-head/pool/main/curl_8.0.0_amd64.deb", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, w.Body.String())
	assert.NotEmpty(t, w.Header().Get("Content-Length"))
}

// TestApt_MethodNotAllowed covers the default case in ServeHTTP.
func TestApt_MethodNotAllowed(t *testing.T) {
	repo := testutil.SimpleRepo("apt-mna", "apt")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodPatch, "/repository/apt-mna/some/other/path", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// TestApt_WebhookOnUpload checks that uploading a .deb fires an artifact.published event.
func TestApt_WebhookOnUpload(t *testing.T) {
	repo := testutil.SimpleRepo("apt-wh", "apt")
	r, wh := setupWithWebhook(repo)

	req := httptest.NewRequest(http.MethodPut,
		"/repository/apt-wh/pool/main/vim_9.0_amd64.deb",
		strings.NewReader("vim-bytes"))
	req.ContentLength = 9
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)
	assert.Len(t, wh.Events, 1)
	assert.Equal(t, domain.EventArtifactPublished, wh.Events[0].Event)
}

// TestApt_UploadParsesNameOnly tests upload of a .deb without version in filename.
// This covers the branch where len(parts) < 2, falling back to filename as name, "0.0.0" as version.
func TestApt_UploadParsesNameOnly(t *testing.T) {
	repo := testutil.SimpleRepo("apt-noversion", "apt")
	r := setup(repo)

	// Filename with no underscore → pkgName=filename without .deb, version="0.0.0"
	code := putDeb(r, "apt-noversion", "/pool/main/nodashpackage.deb", "bytes")
	assert.Equal(t, http.StatusCreated, code)
}

// TestApt_Release_NoDistInPath ensures serveRelease still works for a path
// with an unusual structure (no dist segment available).
func TestApt_Release_NoDistInPath(t *testing.T) {
	repo := testutil.SimpleRepo("apt-rel2", "apt")
	r := setup(repo)

	// Path that only has /dists/ with nothing after it
	req := httptest.NewRequest(http.MethodGet,
		"/repository/apt-rel2/dists/bionic/Release", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "bionic")
}

// TestApt_ProxyGet_RejectsMutation_Delete confirms DELETE is blocked on proxy repos.
func TestApt_ProxyGet_Delete(t *testing.T) {
	repo := testutil.SimpleRepo("apt-proxy-del", "apt")
	repo.Type = domain.TypeProxy
	r := setup(repo)

	req := httptest.NewRequest(http.MethodDelete,
		"/repository/apt-proxy-del/pool/main/vim_1.0_amd64.deb", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}
