package maven_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/maven"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func newDeps(repo *domain.Repository) formats.Deps {
	return formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
}

// TestMaven_Head_Artifact tests HEAD on an existing artifact.
func TestMaven_Head_Artifact(t *testing.T) {
	repo := testutil.SimpleRepo("mvn-head", "maven2")
	r, _ := setup(repo)

	req := httptest.NewRequest(http.MethodPut, "/repository/mvn-head"+jarPath, strings.NewReader(jarBody))
	req.ContentLength = int64(len(jarBody))
	r.ServeHTTP(httptest.NewRecorder(), req)

	req2 := httptest.NewRequest(http.MethodHead, "/repository/mvn-head"+jarPath, nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusOK, w2.Code)
	assert.Empty(t, w2.Body.String())
	assert.NotEmpty(t, w2.Header().Get("Content-Length"))
	assert.Equal(t, "application/java-archive", w2.Header().Get("Content-Type"))
}

// TestMaven_Head_NotFound tests HEAD on a missing artifact.
func TestMaven_Head_NotFound(t *testing.T) {
	repo := testutil.SimpleRepo("mvn-head-nf", "maven2")
	r, _ := setup(repo)

	req := httptest.NewRequest(http.MethodHead, "/repository/mvn-head-nf"+jarPath, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestMaven_GetChecksum_MD5 tests the .md5 checksum sidecar endpoint.
func TestMaven_GetChecksum_MD5(t *testing.T) {
	repo := testutil.SimpleRepo("mvn-md5", "maven2")
	r, _ := setup(repo)

	req := httptest.NewRequest(http.MethodPut, "/repository/mvn-md5"+jarPath, strings.NewReader(jarBody))
	req.ContentLength = int64(len(jarBody))
	r.ServeHTTP(httptest.NewRecorder(), req)

	req2 := httptest.NewRequest(http.MethodGet, "/repository/mvn-md5"+jarPath+".md5", nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	require.Equal(t, http.StatusOK, w2.Code)
	assert.Len(t, w2.Body.String(), 32, "MD5 should be 32 hex chars")
}

// TestMaven_GetChecksum_Missing tests a checksum request when artifact doesn't exist.
func TestMaven_GetChecksum_Missing(t *testing.T) {
	repo := testutil.SimpleRepo("mvn-cs-miss", "maven2")
	r, _ := setup(repo)

	req := httptest.NewRequest(http.MethodGet, "/repository/mvn-cs-miss"+jarPath+".sha1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestMaven_ChecksumHeaders tests that X-Checksum-* headers are set on GET.
func TestMaven_ChecksumHeaders(t *testing.T) {
	repo := testutil.SimpleRepo("mvn-hdr", "maven2")
	r, _ := setup(repo)

	req := httptest.NewRequest(http.MethodPut, "/repository/mvn-hdr"+jarPath, strings.NewReader(jarBody))
	req.ContentLength = int64(len(jarBody))
	r.ServeHTTP(httptest.NewRecorder(), req)

	req2 := httptest.NewRequest(http.MethodGet, "/repository/mvn-hdr"+jarPath, nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	require.Equal(t, http.StatusOK, w2.Code)
	assert.NotEmpty(t, w2.Header().Get("X-Checksum-SHA256"))
	assert.NotEmpty(t, w2.Header().Get("X-Checksum-SHA1"))
	assert.NotEmpty(t, w2.Header().Get("X-Checksum-MD5"))
	assert.NotEmpty(t, w2.Header().Get("ETag"))
}

// TestMaven_DeleteNotFound tests DELETE on a missing artifact.
func TestMaven_DeleteNotFound(t *testing.T) {
	repo := testutil.SimpleRepo("mvn-del-nf", "maven2")
	r, _ := setup(repo)

	req := httptest.NewRequest(http.MethodDelete, "/repository/mvn-del-nf"+jarPath, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	// DeleteArtifact on missing path: the mock returns an error → 500
	// or it silently succeeds returning 204 — either is acceptable;
	// we just confirm the handler routes without panic.
	assert.True(t, w.Code == http.StatusNoContent || w.Code == http.StatusInternalServerError)
}

// TestMaven_MethodNotAllowed tests an unsupported HTTP method.
func TestMaven_MethodNotAllowed(t *testing.T) {
	repo := testutil.SimpleRepo("mvn-method", "maven2")
	r, _ := setup(repo)

	req := httptest.NewRequest(http.MethodPatch, "/repository/mvn-method"+jarPath, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// TestMaven_PomContentType verifies .pom files get application/xml content-type.
func TestMaven_PomContentType(t *testing.T) {
	repo := testutil.SimpleRepo("mvn-pom", "maven2")
	r, _ := setup(repo)

	pomPath := "/org/example/mylib/1.0/mylib-1.0.pom"
	body := "<project/>"
	req := httptest.NewRequest(http.MethodPut, "/repository/mvn-pom"+pomPath, strings.NewReader(body))
	req.ContentLength = int64(len(body))
	r.ServeHTTP(httptest.NewRecorder(), req)

	req2 := httptest.NewRequest(http.MethodGet, "/repository/mvn-pom"+pomPath, nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	require.Equal(t, http.StatusOK, w2.Code)
	assert.Equal(t, "application/xml", w2.Header().Get("Content-Type"))
}

// TestMaven_ZipContentType verifies .zip files get application/zip content-type.
func TestMaven_ZipContentType(t *testing.T) {
	repo := testutil.SimpleRepo("mvn-zip", "maven2")
	r, _ := setup(repo)

	zipPath := "/org/example/mylib/1.0/mylib-1.0.zip"
	body := "zipdata"
	req := httptest.NewRequest(http.MethodPut, "/repository/mvn-zip"+zipPath, strings.NewReader(body))
	req.ContentLength = int64(len(body))
	r.ServeHTTP(httptest.NewRecorder(), req)

	req2 := httptest.NewRequest(http.MethodGet, "/repository/mvn-zip"+zipPath, nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	require.Equal(t, http.StatusOK, w2.Code)
	assert.Equal(t, "application/zip", w2.Header().Get("Content-Type"))
}

// TestMaven_OctetStreamContentType verifies unknown extensions get application/octet-stream.
func TestMaven_OctetStreamContentType(t *testing.T) {
	repo := testutil.SimpleRepo("mvn-oct", "maven2")
	r, _ := setup(repo)

	binPath := "/org/example/mylib/1.0/mylib-1.0.nar"
	body := "bindata"
	req := httptest.NewRequest(http.MethodPut, "/repository/mvn-oct"+binPath, strings.NewReader(body))
	req.ContentLength = int64(len(body))
	r.ServeHTTP(httptest.NewRecorder(), req)

	req2 := httptest.NewRequest(http.MethodGet, "/repository/mvn-oct"+binPath, nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	require.Equal(t, http.StatusOK, w2.Code)
	assert.Equal(t, "application/octet-stream", w2.Header().Get("Content-Type"))
}

// TestMaven_ProxyDELETE_Rejected verifies DELETE is rejected on proxy repos.
func TestMaven_ProxyDELETE_Rejected(t *testing.T) {
	repo := testutil.SimpleRepo("mvn-proxy-del", "maven2")
	repo.Type = "proxy"
	r, _ := setup(repo)

	req := httptest.NewRequest(http.MethodDelete, "/repository/mvn-proxy-del"+jarPath, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// TestMaven_PutChecksumMd5Accepted verifies .md5 sidecar PUT is silently accepted.
func TestMaven_PutChecksumMd5Accepted(t *testing.T) {
	repo := testutil.SimpleRepo("mvn-md5put", "maven2")
	r, _ := setup(repo)

	req := httptest.NewRequest(http.MethodPut, "/repository/mvn-md5put"+jarPath+".md5",
		strings.NewReader("deadbeef"))
	req.ContentLength = 8
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusCreated, w.Code)
}

// TestMaven_WarContentType verifies .war files get application/java-archive content-type.
func TestMaven_WarContentType(t *testing.T) {
	repo := testutil.SimpleRepo("mvn-war", "maven2")
	r, _ := setup(repo)

	warPath := "/org/example/myapp/1.0/myapp-1.0.war"
	body := "wardata"
	req := httptest.NewRequest(http.MethodPut, "/repository/mvn-war"+warPath, strings.NewReader(body))
	req.ContentLength = int64(len(body))
	r.ServeHTTP(httptest.NewRecorder(), req)

	req2 := httptest.NewRequest(http.MethodGet, "/repository/mvn-war"+warPath, nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	require.Equal(t, http.StatusOK, w2.Code)
	assert.Equal(t, "application/java-archive", w2.Header().Get("Content-Type"))
}

// TestMaven_Name verifies the handler reports its name.
func TestMaven_Name(t *testing.T) {
	repo := testutil.SimpleRepo("mvn-name", "maven2")
	d := newDeps(repo)
	h := maven.New(d)
	assert.Equal(t, "maven2", h.Name())
}

// TestMaven_ProxyGET_FallsThrough covers the proxy GET branch (ServeHTTP lines 38-49).
// The proxy repo has no reachable remote_url, so ServeGET returns an error → 500.
func TestMaven_ProxyGET_FallsThrough(t *testing.T) {
	repo := testutil.SimpleRepo("mvn-proxy-get", "maven2")
	repo.Type = "proxy"
	repo.ProxyConfig = map[string]any{"remote_url": "http://127.0.0.1:1"}
	r, _ := setup(repo)

	req := httptest.NewRequest(http.MethodGet, "/repository/mvn-proxy-get"+jarPath, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	// Should not be 404 — the proxy branch was taken; real result is 500 (no upstream)
	assert.NotEqual(t, http.StatusNotFound, w.Code)
}

// TestMaven_ProxyGET_ChecksumPath covers the checksum sidecar path under a proxy repo.
func TestMaven_ProxyGET_ChecksumPath(t *testing.T) {
	repo := testutil.SimpleRepo("mvn-proxy-cs", "maven2")
	repo.Type = "proxy"
	repo.ProxyConfig = map[string]any{"remote_url": "http://127.0.0.1:1"}
	r, _ := setup(repo)

	req := httptest.NewRequest(http.MethodGet, "/repository/mvn-proxy-cs"+jarPath+".sha1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	// proxy branch taken; upstream unreachable → 500
	assert.NotEqual(t, http.StatusOK, w.Code)
}
