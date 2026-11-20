package raw_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/raw"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// proxyRepo builds a raw proxy repository pointing at remote.
func proxyRepo(name, remote string) *domain.Repository {
	return &domain.Repository{
		ID:          "raw-proxy-1",
		Name:        name,
		Format:      "raw",
		Type:        domain.TypeProxy,
		Online:      true,
		ProxyConfig: map[string]any{"remote_url": remote},
	}
}

// nodejsLikeListing is the shape of an upstream directory index that links
// its folders relatively and its files with absolute paths rooted at the
// upstream's own base path — the shape nodejs.org/dist serves, and the one
// that made every file link 404 behind the proxy (#774).
const nodejsLikeListing = `<!DOCTYPE html><html><head><title>Index of /dist/</title></head><body><h1>Index of /dist/</h1><hr><pre>
<a href="../">../</a>
<a href="latest-argon/">latest-argon/</a>
<a href="/dist/index.json">index.json</a>
<a href="/dist/node-v0.6.13.tar.gz">node-v0.6.13.tar.gz</a>
</pre></body></html>`

// setupProxy mounts the raw handler for a proxy repository.
func setupProxy(repo *domain.Repository) *gin.Engine {
	return setupProxyWithBaseURL(repo, "http://localhost:8080")
}

// setupProxyWithBaseURL mounts the raw handler for a proxy repository with an
// explicit configured base URL — the value http.base_url carries.
func setupProxyWithBaseURL(repo *domain.Repository, baseURL string) *gin.Engine {
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    baseURL,
	}
	h := raw.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	return r
}

// The upstream links its files with absolute paths rooted at its own base
// path ("/dist/…"). Served verbatim they escape "/repository/<repo>/" and
// resolve against the local origin, so the repository segment is lost and
// the upstream prefix leaks: the link 404s and nothing can be downloaded.
func TestRawProxy_ListingLinksPointAtTheRepository(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/dist" && r.URL.Path != "/dist/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(nodejsLikeListing))
	}))
	defer upstream.Close()

	r := setupProxy(proxyRepo("myraw-proxy", upstream.URL+"/dist"))

	req := httptest.NewRequest(http.MethodGet, "/repository/myraw-proxy/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	// The absolute upstream file links are re-rooted at this repository.
	assert.Contains(t, body, `href="/repository/myraw-proxy/index.json"`)
	assert.Contains(t, body, `href="/repository/myraw-proxy/node-v0.6.13.tar.gz"`)

	// A relative folder link already resolves through the proxy — it is left
	// alone.
	assert.Contains(t, body, `href="latest-argon/"`)
}

// A link in a proxied listing must not name a host.
//
// The re-rooted links are root-relative: the browser resolves them against the
// origin it reached us on. An absolute link would have to name a host, and the
// only host this server knows is the one in http.base_url — which ships as
// http://localhost:8081 and, on a default install, names a host that is not
// the one the user is browsing: the link would carry the user off the site
// they opened (#774, reported against #776).
func TestRawProxy_ListingCarriesNoForeignHost(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(nodejsLikeListing))
	}))
	defer upstream.Close()

	// A configured public base URL must not leak into the listing either.
	r := setupProxyWithBaseURL(proxyRepo("myraw-proxy", upstream.URL+"/dist"), "https://myrepo.ru:8081")

	req := httptest.NewRequest(http.MethodGet, "/repository/myraw-proxy/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	// The links name the repository and nothing else: no host, no scheme.
	assert.Contains(t, body, `href="/repository/myraw-proxy/index.json"`)
	assert.Contains(t, body, `href="/repository/myraw-proxy/node-v0.6.13.tar.gz"`)
	assert.NotContains(t, body, "myrepo.ru")
	assert.NotContains(t, body, "localhost")
	assert.NotContains(t, body, "http://")
	assert.NotContains(t, body, "https://")
}

// A file that is not HTML is served byte for byte: the rewrite must not
// touch a binary that happens to contain something that looks like a link.
func TestRawProxy_NonHTMLIsServedVerbatim(t *testing.T) {
	payload := []byte("binary\x00href=\"/dist/index.json\"\x00payload")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(payload)
	}))
	defer upstream.Close()

	r := setupProxy(proxyRepo("raw-bin", upstream.URL+"/dist"))

	req := httptest.NewRequest(http.MethodGet, "/repository/raw-bin/blob.tar.gz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, payload, w.Body.Bytes())
}

// The rewrite of a single link, on its own.
func TestRaw_RewriteListingLinks(t *testing.T) {
	rewrite := raw.ListingRewrite{
		LocalPath:        "/repository/myraw-proxy",
		UpstreamHost:     "nodejs.org",
		UpstreamBasePath: "/dist",
	}

	cases := []struct {
		name        string
		href        string
		requestPath string
		want        string
	}{
		{name: "absolute path under the base", href: "/dist/index.json", want: "/repository/myraw-proxy/index.json"},
		{name: "absolute path to the base itself", href: "/dist", want: "/repository/myraw-proxy/"},
		{name: "absolute url to the upstream", href: "https://nodejs.org/dist/index.json", want: "/repository/myraw-proxy/index.json"},
		{name: "absolute url to the upstream, http", href: "http://nodejs.org/dist/index.json", want: "/repository/myraw-proxy/index.json"},
		{name: "absolute path outside the base", href: "/other/index.json", want: "/other/index.json"},
		{name: "absolute url to another host", href: "https://example.com/dist/index.json", want: "https://example.com/dist/index.json"},
		{name: "relative link", href: "latest-argon/", want: "latest-argon/"},
		{name: "relative parent link at the repository root", href: "../", want: "/repository/myraw-proxy/"},
		{name: "relative parent link deeper down", href: "../", requestPath: "/v20.0.0/", want: "../"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rewrite.RequestPath = tc.requestPath
			body := []byte(`<a href="` + tc.href + `">x</a>`)
			out := string(rewrite.Apply(body))
			assert.Contains(t, out, `href="`+tc.want+`"`)
		})
	}
}

// An upstream whose remote URL has no path of its own: every absolute link
// belongs to the repository.
func TestRaw_RewriteListingLinks_NoUpstreamBasePath(t *testing.T) {
	rewrite := raw.ListingRewrite{
		LocalPath:        "/repository/myraw-proxy",
		UpstreamHost:     "example.com",
		UpstreamBasePath: "",
	}

	body := []byte(`<a href="/index.json">x</a>`)
	out := string(rewrite.Apply(body))
	assert.Contains(t, out, `href="/repository/myraw-proxy/index.json"`)
}

// A body that is not HTML comes back unchanged.
func TestRaw_RewriteListingLinks_LeavesOtherBodiesAlone(t *testing.T) {
	rewrite := raw.ListingRewrite{
		LocalPath:        "/repository/myraw-proxy",
		UpstreamHost:     "nodejs.org",
		UpstreamBasePath: "/dist",
	}
	body := []byte(`{"url": "https://nodejs.org/dist/index.json"}`)
	assert.Equal(t, body, rewrite.Apply(body))
}

// The upstream link the user reported: the link must name the repository.
func TestRawProxy_ListingLinkMatchesTheReportedCase(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<a href="/dist/index.json">index.json</a>`))
	}))
	defer upstream.Close()

	r := setupProxy(proxyRepo("myraw-proxy", upstream.URL+"/dist"))

	req := httptest.NewRequest(http.MethodGet, "/repository/myraw-proxy/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "/repository/myraw-proxy/index.json")
	assert.NotContains(t, w.Body.String(), `href="/dist/index.json"`)
	assert.NotContains(t, w.Body.String(), "localhost")
}
