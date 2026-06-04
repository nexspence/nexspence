package cargo_test

import (
	"bytes"
	"encoding/binary"
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
	"github.com/nexspence-oss/nexspence/internal/formats/cargo"
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
	h := cargo.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	return r, wh
}

// TestCargo_Name verifies the Name() method.
func TestCargo_Name(t *testing.T) {
	d := formats.Deps{
		Repos:     testutil.NewRepoRepo(),
		Blobs:     testutil.NewBlobStoreRepo(),
		BlobStore: testutil.NewBlobStore(),
	}
	h := cargo.New(d)
	assert.Equal(t, "cargo", h.Name())
}

// TestCargo_MethodNotAllowed covers the default case in ServeHTTP.
func TestCargo_MethodNotAllowed(t *testing.T) {
	repo := testutil.SimpleRepo("cargo-mna", "cargo")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodPatch, "/repository/cargo-mna/some/path", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// TestCargo_IndexEntry_NotFound exercises the not-found branch of serveIndexEntry.
func TestCargo_IndexEntry_NotFound(t *testing.T) {
	repo := testutil.SimpleRepo("cargo-idx-nf", "cargo")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/cargo-idx-nf/index/no/su/nosuchcrate", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestCargo_Yank_InvalidPath exercises the "not enough path segments" branch in handleYank.
func TestCargo_Yank_InvalidPath(t *testing.T) {
	repo := testutil.SimpleRepo("cargo-yank-bad", "cargo")
	r := setup(repo)

	// Path that resolves to DELETE /api/v1/crates/yank (no name/version segments)
	req := httptest.NewRequest(http.MethodDelete,
		"/repository/cargo-yank-bad/api/v1/crates/yank", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestCargo_Download_InvalidPath exercises the "not enough path segments" branch in handleDownload.
func TestCargo_Download_InvalidPath(t *testing.T) {
	repo := testutil.SimpleRepo("cargo-dl-bad", "cargo")
	r := setup(repo)

	// /download but no name/version before it
	req := httptest.NewRequest(http.MethodGet,
		"/repository/cargo-dl-bad/api/v1/crates/download", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	// handleDownload is only reached when path ends with /download, which it does here.
	// rest after trim = "" → SplitN gives 1 part → bad request
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestCargo_Publish_BadMetaLen covers the "cannot read metadata" error branch.
// We send a body where the meta-length field says 100 bytes but only 3 are provided.
func TestCargo_Publish_BadMetaLen(t *testing.T) {
	repo := testutil.SimpleRepo("cargo-pub-bad", "cargo")
	r := setup(repo)

	var buf bytes.Buffer
	// Claim 100 bytes of metadata but provide none
	_ = binary.Write(&buf, binary.LittleEndian, uint32(100))
	buf.WriteString("ab") // only 2 bytes, far less than claimed 100

	req := httptest.NewRequest(http.MethodPut,
		"/repository/cargo-pub-bad/api/v1/crates/new", &buf)
	req.ContentLength = int64(buf.Len())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestCargo_Publish_BadMetaJSON covers the "invalid metadata JSON" error branch.
func TestCargo_Publish_BadMetaJSON(t *testing.T) {
	repo := testutil.SimpleRepo("cargo-pub-badjson", "cargo")
	r := setup(repo)

	var buf bytes.Buffer
	badJSON := []byte("{not valid json}")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(len(badJSON)))
	buf.Write(badJSON)

	req := httptest.NewRequest(http.MethodPut,
		"/repository/cargo-pub-badjson/api/v1/crates/new", &buf)
	req.ContentLength = int64(buf.Len())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestCargo_Publish_BadCrateLen covers the "cannot read crate length" branch
// (valid meta JSON but body truncated before the crate-length uint32).
func TestCargo_Publish_BadCrateLen(t *testing.T) {
	repo := testutil.SimpleRepo("cargo-pub-nocrate", "cargo")
	r := setup(repo)

	meta := map[string]any{"name": "mylib", "vers": "0.1.0"}
	metaJSON, _ := json.Marshal(meta)

	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.LittleEndian, uint32(len(metaJSON)))
	buf.Write(metaJSON)
	// Body ends here — no crate-length uint32 present

	req := httptest.NewRequest(http.MethodPut,
		"/repository/cargo-pub-nocrate/api/v1/crates/new", &buf)
	req.ContentLength = int64(buf.Len())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestCargo_Publish_EmptyBody covers the initial readU32LE error branch.
func TestCargo_Publish_EmptyBody(t *testing.T) {
	repo := testutil.SimpleRepo("cargo-pub-empty", "cargo")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodPut,
		"/repository/cargo-pub-empty/api/v1/crates/new", bytes.NewReader(nil))
	req.ContentLength = 0
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestCargo_WebhookOnPublish checks that publishing a crate fires an artifact.published event.
func TestCargo_WebhookOnPublish(t *testing.T) {
	repo := testutil.SimpleRepo("cargo-wh", "cargo")
	r, wh := setupWithWebhook(repo)

	body := buildPublishBody("async-std", "1.12.0", "async-std-crate-bytes")
	req := httptest.NewRequest(http.MethodPut,
		"/repository/cargo-wh/api/v1/crates/new", bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Len(t, wh.Events, 1)
	assert.Equal(t, domain.EventArtifactPublished, wh.Events[0].Event)
}

// TestCargo_ProxyIndexConfig covers the proxy branch that still serves index config locally.
func TestCargo_ProxyIndexConfig(t *testing.T) {
	repo := testutil.SimpleRepo("cargo-proxy-cfg", "cargo")
	repo.Type = domain.TypeProxy
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/cargo-proxy-cfg/index/config.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"dl"`)
}

// TestCargo_ProxyRejectsMutation exercises the proxy RejectMutation branch.
func TestCargo_ProxyRejectsMutation(t *testing.T) {
	repo := testutil.SimpleRepo("cargo-proxy-mut", "cargo")
	repo.Type = domain.TypeProxy
	r := setup(repo)

	body := buildPublishBody("lib", "1.0.0", "bytes")
	req := httptest.NewRequest(http.MethodPut,
		"/repository/cargo-proxy-mut/api/v1/crates/new", bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// TestCargo_Search_Empty verifies search with no matching crates returns empty list.
func TestCargo_Search_Empty(t *testing.T) {
	repo := testutil.SimpleRepo("cargo-search-empty", "cargo")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/cargo-search-empty/api/v1/crates?q=nonexistent", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	crates, ok := resp["crates"].([]interface{})
	require.True(t, ok)
	assert.Empty(t, crates)
}

// TestCargo_IndexEntry_WithChecksums verifies that after publishing a crate
// the sparse index entry includes a checksum.
func TestCargo_IndexEntry_WithChecksums(t *testing.T) {
	repo := testutil.SimpleRepo("cargo-idx-ck", "cargo")
	r := setup(repo)

	body := buildPublishBody("hashbrown", "0.14.0", "hashbrown-data")
	pubReq := httptest.NewRequest(http.MethodPut,
		"/repository/cargo-idx-ck/api/v1/crates/new", bytes.NewReader(body))
	pubReq.ContentLength = int64(len(body))
	r.ServeHTTP(httptest.NewRecorder(), pubReq)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/cargo-idx-ck/index/ha/sh/hashbrown", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// Parse first line as NDJSON
	var entry map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.Split(w.Body.String(), "\n")[0]), &entry))
	assert.Equal(t, "hashbrown", entry["name"])
	assert.NotEmpty(t, entry["cksum"])
}
