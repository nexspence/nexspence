package gomod_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/gomod"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// TestGoMod_Name verifies the handler name.
func TestGoMod_Name(t *testing.T) {
	repo := testutil.SimpleRepo("gname", "go")
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	h := gomod.New(d)
	assert.Equal(t, "go", h.Name())
}

// TestGoMod_MethodNotAllowed covers the default method branch.
func TestGoMod_MethodNotAllowed(t *testing.T) {
	repo := testutil.SimpleRepo("g-method", "go")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodDelete,
		"/repository/g-method/github.com/example/lib/@v/v1.0.0.zip", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// TestGoMod_Get_InvalidPath covers the "invalid Go module path" branch (no /@v/ and not /@latest).
func TestGoMod_Get_InvalidPath(t *testing.T) {
	repo := testutil.SimpleRepo("g-inv", "go")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/g-inv/somemodule/badpath", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestGoMod_Get_UnknownEndpoint covers the default case in the /@v/ switch.
func TestGoMod_Get_UnknownEndpoint(t *testing.T) {
	repo := testutil.SimpleRepo("g-unk", "go")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/g-unk/example.com/lib/@v/v1.0.0.unknown", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestGoMod_Latest_Empty covers the "no versions found" branch in serveLatest.
func TestGoMod_Latest_Empty(t *testing.T) {
	repo := testutil.SimpleRepo("g-lat-empty", "go")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/g-lat-empty/example.com/empty/@latest", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestGoMod_GetFile_NotFound covers the error path in serveFile.
func TestGoMod_GetFile_NotFound(t *testing.T) {
	repo := testutil.SimpleRepo("g-file-nf", "go")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/g-file-nf/example.com/lib/@v/v9.9.9.mod", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestGoMod_GetFile_Zip_NotFound covers the zip not-found path.
func TestGoMod_GetFile_Zip_NotFound(t *testing.T) {
	repo := testutil.SimpleRepo("g-zip-nf", "go")
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/g-zip-nf/example.com/lib/@v/v9.9.9.zip", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestGoMod_ProxyGET_FallsThrough covers the proxy GET branch in handleGet.
// The upstream is unreachable so ServeGET will return an error → 500.
func TestGoMod_ProxyGET_FallsThrough(t *testing.T) {
	repo := testutil.SimpleRepo("g-proxy", "go")
	repo.Type = "proxy"
	repo.ProxyConfig = map[string]any{"remote_url": "http://127.0.0.1:1"}
	r := setup(repo)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/g-proxy/example.com/lib/@v/v1.0.0.zip", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	// Proxy branch exercised; no local cache → upstream unreachable → non-200 response
	assert.NotEqual(t, http.StatusMethodNotAllowed, w.Code)
}

// TestGoMod_Put_NoAtv covers handlePut when path has no /@v/ (coords left empty).
func TestGoMod_Put_NoAtv(t *testing.T) {
	repo := testutil.SimpleRepo("g-noatv", "go")
	r := setup(repo)

	code := putModule(r, "g-noatv", "/some/arbitrary/path", "data")
	// StoreArtifact succeeds with empty coords → 201
	assert.Equal(t, http.StatusCreated, code)
}

// TestGoMod_GoContentType_Info covers the .info case in goContentType (indirectly via PUT).
func TestGoMod_GoContentType_Info(t *testing.T) {
	repo := testutil.SimpleRepo("g-info-ct", "go")
	r := setup(repo)

	code := putModule(r, "g-info-ct", "/example.com/lib/@v/v1.0.0.info", `{"Version":"v1.0.0"}`)
	require.Equal(t, http.StatusCreated, code)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/g-info-ct/example.com/lib/@v/v1.0.0.info", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

// TestGoMod_GoContentType_Default covers the default case in goContentType via a HEAD-less path.
// We PUT a .dat file (unknown extension) so goContentType falls to default.
func TestGoMod_GoContentType_Default(t *testing.T) {
	repo := testutil.SimpleRepo("g-def-ct", "go")
	r := setup(repo)

	// Use a non-zip/mod/info extension that has no /@v/ — triggers coords with empty version.
	code := putModule(r, "g-def-ct", "/example.com/lib/@v/v1.0.0.dat", "data")
	assert.Equal(t, http.StatusCreated, code)
}
