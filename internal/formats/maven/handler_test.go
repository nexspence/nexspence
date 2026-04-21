package maven_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/formats/maven"
	"github.com/nexspence-oss/nexspence/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() { gin.SetMode(gin.TestMode) }

func setup(repo *domain.Repository) (*gin.Engine, *testutil.BlobStore) {
	repos := testutil.NewRepoRepo(repo)
	blobs := testutil.NewBlobStoreRepo()
	comps := testutil.NewComponentRepo()
	assets := testutil.NewAssetRepo()
	blobStore := testutil.NewBlobStore()

	d := formats.Deps{
		Repos: repos, Blobs: blobs, Components: comps,
		Assets: assets, BlobStore: blobStore,
		BaseURL: "http://localhost:8080",
	}
	h := maven.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	return r, blobStore
}

const jarPath = "/org/example/mylib/1.0/mylib-1.0.jar"
const jarBody = "fake-jar-bytes"

func TestMaven_PutThenGet(t *testing.T) {
	repo := testutil.SimpleRepo("mvn-central", "maven2")
	r, _ := setup(repo)

	req := httptest.NewRequest(http.MethodPut, "/repository/mvn-central"+jarPath, strings.NewReader(jarBody))
	req.ContentLength = int64(len(jarBody))
	r.ServeHTTP(httptest.NewRecorder(), req)

	req2 := httptest.NewRequest(http.MethodGet, "/repository/mvn-central"+jarPath, nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusOK, w2.Code)
	assert.Equal(t, "application/java-archive", w2.Header().Get("Content-Type"))
	assert.Equal(t, jarBody, w2.Body.String())
}

func TestMaven_PutReturnsCreated(t *testing.T) {
	repo := testutil.SimpleRepo("mvn-put", "maven2")
	r, _ := setup(repo)

	req := httptest.NewRequest(http.MethodPut, "/repository/mvn-put"+jarPath, strings.NewReader(jarBody))
	req.ContentLength = int64(len(jarBody))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestMaven_GetNotFound(t *testing.T) {
	repo := testutil.SimpleRepo("mvn-empty", "maven2")
	r, _ := setup(repo)

	req := httptest.NewRequest(http.MethodGet, "/repository/mvn-empty/com/foo/bar/1.0/bar-1.0.jar", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestMaven_ChecksumSidecarAccepted(t *testing.T) {
	repo := testutil.SimpleRepo("mvn-cs", "maven2")
	r, _ := setup(repo)

	// Client-uploaded .sha1 sidecars are silently accepted (we compute our own)
	req := httptest.NewRequest(http.MethodPut, "/repository/mvn-cs"+jarPath+".sha1",
		strings.NewReader("aabbcc"))
	req.ContentLength = 6
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestMaven_GetChecksum_SHA256(t *testing.T) {
	repo := testutil.SimpleRepo("mvn-hash", "maven2")
	r, _ := setup(repo)

	req := httptest.NewRequest(http.MethodPut, "/repository/mvn-hash"+jarPath, strings.NewReader(jarBody))
	req.ContentLength = int64(len(jarBody))
	r.ServeHTTP(httptest.NewRecorder(), req)

	req2 := httptest.NewRequest(http.MethodGet, "/repository/mvn-hash"+jarPath+".sha256", nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	require.Equal(t, http.StatusOK, w2.Code)
	assert.Len(t, w2.Body.String(), 64, "SHA256 should be 64 hex chars")
}

func TestMaven_GetChecksum_SHA1(t *testing.T) {
	repo := testutil.SimpleRepo("mvn-sha1", "maven2")
	r, _ := setup(repo)

	req := httptest.NewRequest(http.MethodPut, "/repository/mvn-sha1"+jarPath, strings.NewReader(jarBody))
	req.ContentLength = int64(len(jarBody))
	r.ServeHTTP(httptest.NewRecorder(), req)

	req2 := httptest.NewRequest(http.MethodGet, "/repository/mvn-sha1"+jarPath+".sha1", nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	require.Equal(t, http.StatusOK, w2.Code)
	assert.Len(t, w2.Body.String(), 40, "SHA1 should be 40 hex chars")
}

func TestMaven_Delete(t *testing.T) {
	repo := testutil.SimpleRepo("mvn-del", "maven2")
	r, blobStore := setup(repo)

	req := httptest.NewRequest(http.MethodPut, "/repository/mvn-del"+jarPath, strings.NewReader(jarBody))
	req.ContentLength = int64(len(jarBody))
	r.ServeHTTP(httptest.NewRecorder(), req)

	key := base.BlobKey("mvn-del", jarPath)
	require.True(t, blobStore.Has(key))

	delReq := httptest.NewRequest(http.MethodDelete, "/repository/mvn-del"+jarPath, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, delReq)
	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.False(t, blobStore.Has(key))
}

func TestMaven_ProxyPUT_Rejected(t *testing.T) {
	repo := &domain.Repository{
		ID: "rp1", Name: "mvn-proxy", Format: "maven2",
		Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": "https://repo1.maven.org/maven2"},
	}
	r, _ := setup(repo)
	req := httptest.NewRequest(http.MethodPut, "/repository/mvn-proxy"+jarPath, strings.NewReader(jarBody))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}
