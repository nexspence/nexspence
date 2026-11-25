package conda_test

// #821: every URL the client is expected to follow must be root-relative.
// The server mints links from http.base_url, which is the server's own
// configuration — behind a reverse proxy it is not the host the client
// reached, so the link points somewhere unreachable. A root-relative path
// carries no host and resolves against the host the client already used.
//
// The conda client resolves a package url against the channel it read
// repodata.json from, so a root-relative path (rather than a path-relative
// one, which would resolve against the directory of repodata.json and
// duplicate the subdir) names the package on the host the client reached.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/conda"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// wrongBaseURL is deliberately not the host the test client reaches.
const wrongBaseURL = "http://wrong.example:8080"

// setupWithBaseURL builds a handler whose configured base URL is baseURL.
func setupWithBaseURL(repo *domain.Repository, baseURL string) *gin.Engine {
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    baseURL,
	}
	h := conda.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	return r
}

// assertNoAbsoluteURL fails when the body carries a URL with a host: the
// client is then told where to go, and that host is not necessarily the one
// it reached.
func assertNoAbsoluteURL(t *testing.T, body string) {
	t.Helper()
	assert.NotContains(t, body, "http://", "the served document must not carry an absolute URL")
	assert.NotContains(t, body, "https://", "the served document must not carry an absolute URL")
	assert.NotContains(t, body, wrongBaseURL, "the configured base URL must not reach the client")
}

// proxyRepo builds a conda proxy repository pointed at remoteURL.
func proxyRepo(name, remoteURL string) *domain.Repository {
	return &domain.Repository{
		ID: name, Name: name, Format: "conda",
		Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": remoteURL},
	}
}

// The proxied index names each package with a root-relative download URL.
func TestConda_Proxy_PackageURLIsRootRelative(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"info":{"subdir":"linux-64"},
"packages":{"numpy-1.24.0-py311_0.tar.bz2":{"name":"numpy","version":"1.24.0",
"url":"pkgs/numpy-1.24.0-py311_0.tar.bz2"}},
"packages.conda":{}}`)
	}))
	defer upstream.Close()

	r := setupWithBaseURL(proxyRepo("conda-rel-proxy", upstream.URL), wrongBaseURL)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/conda-rel-proxy/linux-64/repodata.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var doc struct {
		Packages map[string]struct {
			URL string `json:"url"`
		} `json:"packages"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &doc))
	entry, ok := doc.Packages["numpy-1.24.0-py311_0.tar.bz2"]
	require.True(t, ok, "package entry missing from the rewritten index")
	assert.Equal(t, "/repository/conda-rel-proxy/linux-64/pkgs/numpy-1.24.0-py311_0.tar.bz2",
		entry.URL, "the package URL must be root-relative")
	assertNoAbsoluteURL(t, w.Body.String())
}

// The served index must not depend on the request's Host header. A link built
// from the request's host would be the cache-poisoning vector this fix exists
// to avoid — and it would be invisible: every other test sends the usual
// host, so such a change would still pass them all. The same request served
// under a foreign host must return the very same bytes.
func TestConda_Index_DoesNotDependOnRequestHost(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"info":{"subdir":"linux-64"},
"packages":{"numpy-1.24.0-py311_0.tar.bz2":{"name":"numpy","version":"1.24.0",
"url":"pkgs/numpy-1.24.0-py311_0.tar.bz2"}},
"packages.conda":{}}`)
	}))
	defer upstream.Close()

	r := setupWithBaseURL(proxyRepo("conda-rel-hostdep", upstream.URL), "http://localhost:8080")

	serve := func(host string) []byte {
		req := httptest.NewRequest(http.MethodGet,
			"/repository/conda-rel-hostdep/linux-64/repodata.json", nil)
		req.Host = host
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		return w.Body.Bytes()
	}

	normal := serve("localhost")
	foreign := serve("myrepo.ru")

	assert.Contains(t, string(normal), "/repository/conda-rel-hostdep/linux-64/pkgs/",
		"the comparison must not be vacuous: the index carries links")
	assert.Equal(t, normal, foreign,
		"the served index must not depend on the request's Host header")
}
