package cargo_test

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/cargo"
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
	h := cargo.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	return r
}

// buildPublishBody creates the cargo publish wire format:
// [4-byte LE meta length][meta JSON][4-byte LE crate length][crate bytes]
func buildPublishBody(name, version, crateContent string) []byte {
	meta := map[string]any{"name": name, "vers": version, "deps": []any{}}
	metaJSON, _ := json.Marshal(meta)
	crateBytes := []byte(crateContent)

	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.LittleEndian, uint32(len(metaJSON)))
	buf.Write(metaJSON)
	_ = binary.Write(&buf, binary.LittleEndian, uint32(len(crateBytes)))
	buf.Write(crateBytes)
	return buf.Bytes()
}

func TestCargo_IndexConfig(t *testing.T) {
	repo := testutil.SimpleRepo("crates", "cargo")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet, "/repository/crates/index/config.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"dl"`)
	assert.Contains(t, w.Body.String(), `"api"`)
}

func TestCargo_PublishAndDownload(t *testing.T) {
	repo := testutil.SimpleRepo("crates2", "cargo")
	r := setup(repo)

	body := buildPublishBody("mylib", "0.1.0", "crate-binary-data")
	req := httptest.NewRequest(http.MethodPut, "/repository/crates2/api/v1/crates/new", bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	// Download
	req2 := httptest.NewRequest(http.MethodGet,
		"/repository/crates2/api/v1/crates/mylib/0.1.0/download", nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusOK, w2.Code)
	assert.Equal(t, "crate-binary-data", w2.Body.String())
}

func TestCargo_IndexEntry_AfterPublish(t *testing.T) {
	repo := testutil.SimpleRepo("crates3", "cargo")
	r := setup(repo)

	body := buildPublishBody("serde", "1.0.0", "serde-crate")
	r.ServeHTTP(httptest.NewRecorder(), func() *http.Request {
		req := httptest.NewRequest(http.MethodPut, "/repository/crates3/api/v1/crates/new", bytes.NewReader(body))
		req.ContentLength = int64(len(body))
		return req
	}())

	req := httptest.NewRequest(http.MethodGet, "/repository/crates3/index/se/rd/serde", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "serde")
}

func TestCargo_Search(t *testing.T) {
	repo := testutil.SimpleRepo("crates4", "cargo")
	r := setup(repo)

	body := buildPublishBody("tokio", "1.35.0", "tokio-crate")
	r.ServeHTTP(httptest.NewRecorder(), func() *http.Request {
		req := httptest.NewRequest(http.MethodPut, "/repository/crates4/api/v1/crates/new", bytes.NewReader(body))
		req.ContentLength = int64(len(body))
		return req
	}())

	req := httptest.NewRequest(http.MethodGet, "/repository/crates4/api/v1/crates?q=tokio", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "tokio")
}

func TestCargo_Download_NotFound(t *testing.T) {
	repo := testutil.SimpleRepo("crates5", "cargo")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/crates5/api/v1/crates/nonexistent/1.0.0/download", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestCargo_Yank(t *testing.T) {
	repo := testutil.SimpleRepo("crates6", "cargo")
	r := setup(repo)

	body := buildPublishBody("clap", "4.0.0", "clap-bytes")
	r.ServeHTTP(httptest.NewRecorder(), func() *http.Request {
		req := httptest.NewRequest(http.MethodPut, "/repository/crates6/api/v1/crates/new", bytes.NewReader(body))
		req.ContentLength = int64(len(body))
		return req
	}())

	req := httptest.NewRequest(http.MethodDelete,
		"/repository/crates6/api/v1/crates/clap/4.0.0/yank", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"ok"`)
}
