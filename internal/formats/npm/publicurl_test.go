package npm_test

import (
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
	"github.com/nexspence-oss/nexspence/internal/formats/npm"
	"github.com/nexspence-oss/nexspence/internal/publicurl"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// npm is the one format that cannot do without an absolute URL: npm resolves
// dist.tarball against nothing at all, and a relative one is read as a file
// path on the client's disk (#826). So the tarball URL has to name the host
// the client reached — which behind a reverse proxy is the host the proxy
// says, as long as the proxy is one this instance trusts.

func setupWithProxyTrust(repo *domain.Repository, trusted []string) *gin.Engine {
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
		PublicURL:  publicurl.NewResolver("http://localhost:8080", trusted),
	}
	h := npm.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	return r
}

// publish puts a package into the repository.
func publishPackage(t *testing.T, r *gin.Engine, repo, pkg, version string) {
	t.Helper()
	body := publishBody(pkg, version, "fake-tgz-content")
	req := httptest.NewRequest(http.MethodPut, "/repository/"+repo+"/"+pkg, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
}

// tarballOf reads the dist.tarball the packument advertises.
func tarballOf(t *testing.T, r *gin.Engine, repo, pkg string, remoteAddr string, headers map[string]string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/repository/"+repo+"/"+pkg, nil)
	req.RemoteAddr = remoteAddr
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var doc struct {
		Versions map[string]struct {
			Dist struct {
				Tarball string `json:"tarball"`
			} `json:"dist"`
		} `json:"versions"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &doc))
	for _, v := range doc.Versions {
		return v.Dist.Tarball
	}
	t.Fatal("the packument advertises no version")
	return ""
}

// A request that came through a trusted proxy carries the host the client
// used — that is the host the tarball URL has to name.
func TestNPM_PackumentNamesTheHostATrustedProxyReports(t *testing.T) {
	repo := testutil.SimpleRepo("npm-hosted", "npm")
	r := setupWithProxyTrust(repo, []string{"10.0.0.0/8"})
	publishPackage(t, r, "npm-hosted", "mylib", "1.2.3")

	tarball := tarballOf(t, r, "npm-hosted", "mylib", "10.1.2.3:5000", map[string]string{
		"X-Forwarded-Host": "nexus.example.com",
	})
	assert.Equal(t, "http://nexus.example.com/repository/npm-hosted/mylib/-/mylib-1.2.3.tgz", tarball)
}

// A request that did not come through a trusted proxy keeps the configured
// host, whatever the header says: a client that reaches the instance
// directly must not be able to leave a document behind that sends every
// later client to a host of its choosing.
func TestNPM_PackumentIgnoresTheHostOfAnUntrustedPeer(t *testing.T) {
	repo := testutil.SimpleRepo("npm-hosted", "npm")
	r := setupWithProxyTrust(repo, []string{"10.0.0.0/8"})
	publishPackage(t, r, "npm-hosted", "mylib", "1.2.3")

	tarball := tarballOf(t, r, "npm-hosted", "mylib", "203.0.113.9:5000", map[string]string{
		"X-Forwarded-Host": "evil.example.com",
	})
	assert.Equal(t, "http://localhost:8080/repository/npm-hosted/mylib/-/mylib-1.2.3.tgz", tarball)
}

// Without a resolver — an instance reachable directly — nothing changes.
func TestNPM_PackumentUsesTheConfiguredBaseURLWithoutAResolver(t *testing.T) {
	repo := testutil.SimpleRepo("npm-hosted", "npm")
	r := setup(repo)
	publishPackage(t, r, "npm-hosted", "mylib", "1.2.3")

	tarball := tarballOf(t, r, "npm-hosted", "mylib", "10.1.2.3:5000", map[string]string{
		"X-Forwarded-Host": "nexus.example.com",
	})
	assert.Equal(t, "http://localhost:8080/repository/npm-hosted/mylib/-/mylib-1.2.3.tgz", tarball)
}
