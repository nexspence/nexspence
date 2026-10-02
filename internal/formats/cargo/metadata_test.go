package cargo_test

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/cargo"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// publishMetaBody builds a publish request whose JSON half is meta verbatim.
func publishMetaBody(meta string, crate string) []byte {
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.LittleEndian, uint32(len(meta)))
	buf.WriteString(meta)
	_ = binary.Write(&buf, binary.LittleEndian, uint32(len(crate)))
	buf.WriteString(crate)
	return buf.Bytes()
}

func setupCargoDeps() (*gin.Engine, formats.Deps, *testutil.ComponentRepo) {
	comps := testutil.NewComponentRepo()
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(testutil.SimpleRepo("crates", "cargo")),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: comps,
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	h := cargo.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	return r, d, comps
}

func doPublish(r *gin.Engine, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPut, "/repository/crates/api/v1/crates/new", bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// The publish metadata of rvc-libb from #587: a renamed optional dependency
// and a "dep:" feature.
const libbMeta = `{
  "name": "rvc-libb", "vers": "0.1.0",
  "deps": [{
    "name": "rvc-liba", "version_req": "^0.1", "features": [], "optional": true,
    "default_features": true, "target": null, "kind": "normal", "registry": null,
    "explicit_name_in_toml": "foo"
  }, {
    "name": "serde", "version_req": "^1", "features": ["derive"], "optional": false,
    "default_features": false, "target": "cfg(unix)", "kind": "dev", "registry": "https://github.com/rust-lang/crates.io-index"
  }],
  "features": {"default": [], "extra": [], "withfoo": ["dep:foo"]},
  "links": "z"
}`

func TestCargo_IndexEntryCarriesDepsAndFeatures(t *testing.T) {
	r, _, _ := setupCargoDeps()
	require.Equal(t, http.StatusOK, doPublish(r, publishMetaBody(libbMeta, "crate")).Code)

	req := httptest.NewRequest(http.MethodGet, "/repository/crates/index/rv/c-/rvc-libb", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var rec struct {
		Deps      []map[string]any    `json:"deps"`
		Features  map[string][]string `json:"features"`
		Features2 map[string][]string `json:"features2"`
		V         int                 `json:"v"`
		Links     string              `json:"links"`
	}
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(w.Body.String())), &rec))
	require.Len(t, rec.Deps, 2)

	foo := rec.Deps[0]
	assert.Equal(t, "foo", foo["name"], "a renamed dependency is indexed by its rename")
	assert.Equal(t, "rvc-liba", foo["package"], "and points at the real crate")
	assert.Equal(t, "^0.1", foo["req"])
	assert.Equal(t, true, foo["optional"])
	assert.Equal(t, "normal", foo["kind"])

	serde := rec.Deps[1]
	assert.Equal(t, "serde", serde["name"])
	assert.NotContains(t, serde, "package")
	assert.Equal(t, []any{"derive"}, serde["features"])
	assert.Equal(t, false, serde["default_features"])
	assert.Equal(t, "cfg(unix)", serde["target"])
	assert.Equal(t, "dev", serde["kind"])
	assert.Equal(t, "https://github.com/rust-lang/crates.io-index", serde["registry"])

	assert.Equal(t, map[string][]string{"default": {}, "extra": {}}, rec.Features)
	assert.Equal(t, map[string][]string{"withfoo": {"dep:foo"}}, rec.Features2)
	assert.Equal(t, 2, rec.V, "features2 needs index schema v2")
	assert.Equal(t, "z", rec.Links)
}

// A crate without new-syntax features stays on schema v1.
func TestCargo_IndexEntryWithoutFeatures2StaysV1(t *testing.T) {
	r, _, _ := setupCargoDeps()
	meta := `{"name":"plain","vers":"1.0.0","deps":[],"features":{"std":[]}}`
	require.Equal(t, http.StatusOK, doPublish(r, publishMetaBody(meta, "crate")).Code)

	req := httptest.NewRequest(http.MethodGet, "/repository/crates/index/pl/ai/plain", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var rec map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(w.Body.String())), &rec))
	assert.NotContains(t, rec, "v")
	assert.NotContains(t, rec, "features2")
	assert.Equal(t, map[string]any{"std": []any{}}, rec["features"])
}

// A publish whose metadata cannot be stored fails, so cargo reports it.
func TestCargo_PublishFailsWhenMetadataCannotBeStored(t *testing.T) {
	r, _, comps := setupCargoDeps()
	comps.UpdateExtraErr = errors.New("db down")
	w := doPublish(r, publishMetaBody(libbMeta, "crate"))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
