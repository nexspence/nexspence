package conda_test

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
	"github.com/nexspence-oss/nexspence/internal/formats/conda"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

const condaBaseURL = "http://localhost:8080"

// subpathProxy builds a conda proxy repository pointing at remoteURL.
func subpathProxy(t *testing.T, repoName, remoteURL string) (*gin.Engine, *testutil.ComponentRepo) {
	t.Helper()
	comps := testutil.NewComponentRepo()
	repo := &domain.Repository{
		ID: repoName, Name: repoName, Format: "conda",
		Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": remoteURL},
	}
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: comps,
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    condaBaseURL,
	}
	h := conda.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	return r, comps
}

const condaPkgFile = "numpy-1.24.0-py311_0.tar.bz2"

// subdirUpstream serves repodata.json at repodataPath whose single "packages" entry
// carries entryURL, and serves the package bytes ONLY at pkgPath. Everything else
// 404s, so a rewrite that loses or invents a path segment cannot pass.
// A "%s" in entryURL is replaced with the server's own base URL.
func subdirUpstream(t *testing.T, repodataPath, entryURL, pkgPath, body string) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case repodataPath:
			u := strings.ReplaceAll(entryURL, "%s", srv.URL)
			doc := map[string]any{
				"info": map[string]any{"subdir": "linux-64"},
				"packages": map[string]any{
					condaPkgFile: map[string]any{
						"name":    "numpy",
						"version": "1.24.0",
						"url":     u,
					},
				},
				"packages.conda": map[string]any{},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(doc)
		case pkgPath:
			w.Header().Set("Content-Type", "application/x-tar")
			_, _ = w.Write([]byte(body))
		default:
			http.NotFound(w, r)
		}
	}))
	return srv
}

// firstPackageURL fetches repodata.json through the proxy and returns the rewritten
// download URL a conda client would follow.
func firstPackageURL(t *testing.T, r *gin.Engine, repoName, platform string) string {
	t.Helper()
	return indexPackageURL(t, r, repoName, platform, "repodata.json")
}

// indexPackageURL is firstPackageURL for any of the index documents conda may ask
// for — they share repodata.json's schema, so the same extraction works on all.
func indexPackageURL(t *testing.T, r *gin.Engine, repoName, platform, indexName string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet,
		"/repository/"+repoName+"/"+platform+"/"+indexName, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "%s body: %s", indexName, w.Body.String())

	var doc struct {
		Packages map[string]struct {
			URL string `json:"url"`
		} `json:"packages"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &doc))
	entry, ok := doc.Packages[condaPkgFile]
	require.True(t, ok, "package entry missing from rewritten %s", indexName)
	return entry.URL
}

// fetchThrough requests exactly the URL the rewritten index handed the client.
func fetchThrough(t *testing.T, r *gin.Engine, pkgURL string) *httptest.ResponseRecorder {
	t.Helper()
	require.True(t, strings.HasPrefix(pkgURL, condaBaseURL),
		"expected a proxy URL, got %s", pkgURL)
	req := httptest.NewRequest(http.MethodGet, strings.TrimPrefix(pkgURL, condaBaseURL), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestConda_Proxy_NestedPackageURL_RoundTrip is the reported case (#142): the
// upstream repodata.json points at a package stored below the subdir directory.
// path.Base dropped "pkgs/", so the client asked for a path that does not exist
// upstream. The download path comes out of the rewritten document, so this proves
// the whole round trip closes.
func TestConda_Proxy_NestedPackageURL_RoundTrip(t *testing.T) {
	upstream := subdirUpstream(t, "/linux-64/repodata.json",
		"pkgs/"+condaPkgFile, "/linux-64/pkgs/"+condaPkgFile, "nested-package-bytes")
	defer upstream.Close()

	r, _ := subpathProxy(t, "conda-nested", upstream.URL)

	pkgURL := firstPackageURL(t, r, "conda-nested", "linux-64")
	assert.Equal(t, condaBaseURL+"/repository/conda-nested/linux-64/pkgs/"+condaPkgFile, pkgURL,
		"the upstream subdirectory must survive the rewrite")

	w := fetchThrough(t, r, pkgURL)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	assert.Equal(t, "nested-package-bytes", w.Body.String())
}

// TestConda_Proxy_FlatPackageURL_RoundTrip is the regression guard: the ordinary
// channel layout, where the package sits beside repodata.json, must be unchanged.
func TestConda_Proxy_FlatPackageURL_RoundTrip(t *testing.T) {
	upstream := subdirUpstream(t, "/linux-64/repodata.json",
		condaPkgFile, "/linux-64/"+condaPkgFile, "flat-package-bytes")
	defer upstream.Close()

	r, _ := subpathProxy(t, "conda-flat", upstream.URL)

	pkgURL := firstPackageURL(t, r, "conda-flat", "linux-64")
	assert.Equal(t, condaBaseURL+"/repository/conda-flat/linux-64/"+condaPkgFile, pkgURL)

	w := fetchThrough(t, r, pkgURL)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	assert.Equal(t, "flat-package-bytes", w.Body.String())
}

// TestConda_Proxy_AbsoluteURLUnderRemote_RoundTrip covers a repodata.json that
// spells its URLs out in full against the configured channel: the channel prefix is
// stripped and the remainder — subdirectory included — becomes the proxy path.
func TestConda_Proxy_AbsoluteURLUnderRemote_RoundTrip(t *testing.T) {
	upstream := subdirUpstream(t, "/linux-64/repodata.json",
		"%s/linux-64/pkgs/"+condaPkgFile, "/linux-64/pkgs/"+condaPkgFile, "absolute-package-bytes")
	defer upstream.Close()

	r, _ := subpathProxy(t, "conda-abs", upstream.URL)

	pkgURL := firstPackageURL(t, r, "conda-abs", "linux-64")
	assert.Equal(t, condaBaseURL+"/repository/conda-abs/linux-64/pkgs/"+condaPkgFile, pkgURL)

	w := fetchThrough(t, r, pkgURL)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	assert.Equal(t, "absolute-package-bytes", w.Body.String())
}

// TestConda_Proxy_RootRelativeURL_RoundTrip covers an entry that starts with "/".
// It resolves against the HOST root, not the channel's path prefix, and is proxied
// only because it happens to land back inside the channel subtree.
func TestConda_Proxy_RootRelativeURL_RoundTrip(t *testing.T) {
	upstream := subdirUpstream(t, "/channel/linux-64/repodata.json",
		"/channel/linux-64/pkgs/"+condaPkgFile, "/channel/linux-64/pkgs/"+condaPkgFile,
		"root-relative-package-bytes")
	defer upstream.Close()

	r, _ := subpathProxy(t, "conda-rootrel", upstream.URL+"/channel")

	pkgURL := firstPackageURL(t, r, "conda-rootrel", "linux-64")
	assert.Equal(t, condaBaseURL+"/repository/conda-rootrel/linux-64/pkgs/"+condaPkgFile, pkgURL)

	w := fetchThrough(t, r, pkgURL)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	assert.Equal(t, "root-relative-package-bytes", w.Body.String())
}

// TestConda_Proxy_ForeignHostURL_LeftAlone covers a channel whose repodata.json
// points at a CDN. We cannot express that as a path under this proxy — forwarding it
// would 404 upstream — so the absolute URL must survive untouched.
func TestConda_Proxy_ForeignHostURL_LeftAlone(t *testing.T) {
	const foreign = "https://conda.anaconda.org/conda-forge/linux-64/" + condaPkgFile
	upstream := subdirUpstream(t, "/linux-64/repodata.json",
		foreign, "/unused", "unused")
	defer upstream.Close()

	r, _ := subpathProxy(t, "conda-foreign", upstream.URL)

	assert.Equal(t, foreign, firstPackageURL(t, r, "conda-foreign", "linux-64"),
		"a package on another host cannot be proxied and must not be rewritten")
}

// TestConda_Proxy_SameHostOutsideChannel_LeftAlone covers a channel served under a
// path prefix whose repodata.json points at a sibling channel on the same host. That
// is outside the proxied subtree, so it cannot be expressed as a path here either.
func TestConda_Proxy_SameHostOutsideChannel_LeftAlone(t *testing.T) {
	upstream := subdirUpstream(t, "/channel/linux-64/repodata.json",
		"%s/other-channel/linux-64/"+condaPkgFile, "/unused", "unused")
	defer upstream.Close()

	r, _ := subpathProxy(t, "conda-outside", upstream.URL+"/channel")

	assert.Equal(t, upstream.URL+"/other-channel/linux-64/"+condaPkgFile,
		firstPackageURL(t, r, "conda-outside", "linux-64"),
		"a URL outside the proxied channel is not ours to rewrite")
}

// TestConda_Proxy_CrossSubdirURL_RoundTrip covers an entry pointing at a sibling
// subdir of the same channel ("../noarch/..."). The proxy path is rooted at the
// channel, not at the subdir, so this stays proxyable.
func TestConda_Proxy_CrossSubdirURL_RoundTrip(t *testing.T) {
	const noarchFile = "mypackage-0.1.0-py_0.conda"
	upstream := subdirUpstream(t, "/linux-64/repodata.json",
		"../noarch/"+noarchFile, "/noarch/"+noarchFile, "noarch-package-bytes")
	defer upstream.Close()

	r, _ := subpathProxy(t, "conda-cross", upstream.URL)

	pkgURL := firstPackageURL(t, r, "conda-cross", "linux-64")
	assert.Equal(t, condaBaseURL+"/repository/conda-cross/noarch/"+noarchFile, pkgURL)

	w := fetchThrough(t, r, pkgURL)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	assert.Equal(t, "noarch-package-bytes", w.Body.String())
}

// TestConda_Proxy_ChannelRootURL_LeftAlone covers an entry resolving to the channel
// root itself. Every conda request path is "/<subdir>/<file>", so a single-segment
// path has no proxy URL that would route: hand the client the upstream original
// instead of a proxy URL that answers 400.
func TestConda_Proxy_ChannelRootURL_LeftAlone(t *testing.T) {
	upstream := subdirUpstream(t, "/linux-64/repodata.json",
		"../"+condaPkgFile, "/unused", "unused")
	defer upstream.Close()

	r, _ := subpathProxy(t, "conda-root", upstream.URL)

	assert.Equal(t, upstream.URL+"/"+condaPkgFile,
		firstPackageURL(t, r, "conda-root", "linux-64"),
		"a channel-root URL has no routable proxy path")
}

// TestConda_Proxy_QueryString_LeftAlone covers a signed URL. The download branch
// forwards only the request path upstream, so a rewritten copy would be re-fetched
// without its signature and rejected.
func TestConda_Proxy_QueryString_LeftAlone(t *testing.T) {
	upstream := subdirUpstream(t, "/linux-64/repodata.json",
		"%s/linux-64/"+condaPkgFile+"?token=abc", "/unused", "unused")
	defer upstream.Close()

	r, _ := subpathProxy(t, "conda-query", upstream.URL)

	assert.Equal(t, upstream.URL+"/linux-64/"+condaPkgFile+"?token=abc",
		firstPackageURL(t, r, "conda-query", "linux-64"),
		"a signed URL cannot survive the round trip")
}

// TestConda_Proxy_NestedPackage_CachedCoords verifies the cached component is named
// after the package file, not after the subdirectory path it was fetched from.
func TestConda_Proxy_NestedPackage_CachedCoords(t *testing.T) {
	upstream := subdirUpstream(t, "/linux-64/repodata.json",
		"pkgs/"+condaPkgFile, "/linux-64/pkgs/"+condaPkgFile, "nested-package-bytes")
	defer upstream.Close()

	r, comps := subpathProxy(t, "conda-coords", upstream.URL)

	pkgURL := firstPackageURL(t, r, "conda-coords", "linux-64")
	w := fetchThrough(t, r, pkgURL)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	page, err := comps.List(t.Context(), "conda-coords", 100, 0)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assert.Equal(t, condaPkgFile, page.Items[0].Name,
		"the component name must not carry the subdirectory")
}
