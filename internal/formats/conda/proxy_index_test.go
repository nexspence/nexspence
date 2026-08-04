package conda_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Index documents other than repodata.json (#145). conda clients prefer
// current_repodata.json, and conda-index also publishes repodata_from_packages.json;
// all three carry the same schema and must be rewritten onto this proxy, and none of
// them may be cached as if it were an immutable package.

// countingUpstream serves indexName under /linux-64/ with a single "packages" entry
// naming pkgFile, plus the package bytes at /linux-64/<pkgFile>. The body of the
// index changes on every request (the version counts up) and hits records how many
// requests the whole server saw, so a test can tell a fresh fetch from a cache hit.
func countingUpstream(t *testing.T, indexName, pkgFile string) (srv *httptest.Server, hits *atomic.Int64) {
	t.Helper()
	hits = &atomic.Int64{}
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/linux-64/" + indexName:
			doc := map[string]any{
				"info": map[string]any{"subdir": "linux-64"},
				"packages": map[string]any{
					pkgFile: map[string]any{
						"name": "numpy",
						// A different version per request stands in for a channel
						// that changed: a stale copy is visible in the response.
						"version": fmt.Sprintf("1.24.%d", hits.Load()),
						"url":     pkgFile,
					},
				},
				"packages.conda": map[string]any{},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(doc)
		case "/linux-64/" + pkgFile:
			w.Header().Set("Content-Type", "application/x-tar")
			_, _ = w.Write([]byte("package-bytes"))
		default:
			http.NotFound(w, r)
		}
	}))
	return srv, hits
}

// indexVersion fetches indexName through the proxy and returns the version the
// document advertises for pkgFile.
func indexVersion(t *testing.T, r *gin.Engine, repoName, indexName, pkgFile string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/repository/"+repoName+"/linux-64/"+indexName, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "%s body: %s", indexName, w.Body.String())

	var doc struct {
		Packages map[string]struct {
			Version string `json:"version"`
		} `json:"packages"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &doc))
	entry, ok := doc.Packages[pkgFile]
	require.True(t, ok, "package entry missing from %s", indexName)
	return entry.Version
}

// TestConda_Proxy_CurrentRepodata_RewritesURLs_RoundTrip is the reported case (#145):
// current_repodata.json is what a recent conda client asks for first, and its URLs
// were handed to the client untouched, sending the download straight to the upstream.
// The download path comes out of the rewritten document, so this proves the round trip.
func TestConda_Proxy_CurrentRepodata_RewritesURLs_RoundTrip(t *testing.T) {
	upstream := subdirUpstream(t, "/linux-64/current_repodata.json",
		"pkgs/"+condaPkgFile, "/linux-64/pkgs/"+condaPkgFile, "current-package-bytes")
	defer upstream.Close()

	r, _ := subpathProxy(t, "conda-current", upstream.URL)

	pkgURL := indexPackageURL(t, r, "conda-current", "linux-64", "current_repodata.json")
	assert.Equal(t, condaBaseURL+"/repository/conda-current/linux-64/pkgs/"+condaPkgFile, pkgURL,
		"current_repodata.json must be rewritten the way repodata.json is")

	w := fetchThrough(t, r, pkgURL)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	assert.Equal(t, "current-package-bytes", w.Body.String())
}

// TestConda_Proxy_RepodataFromPackages_RewritesURLs_RoundTrip covers the third
// document conda-index publishes with repodata.json's schema; a client pointed at it
// through repodata_fns has the same claim on rewritten URLs.
func TestConda_Proxy_RepodataFromPackages_RewritesURLs_RoundTrip(t *testing.T) {
	upstream := subdirUpstream(t, "/linux-64/repodata_from_packages.json",
		"pkgs/"+condaPkgFile, "/linux-64/pkgs/"+condaPkgFile, "from-packages-bytes")
	defer upstream.Close()

	r, _ := subpathProxy(t, "conda-fromjson", upstream.URL)

	pkgURL := indexPackageURL(t, r, "conda-fromjson", "linux-64", "repodata_from_packages.json")
	assert.Equal(t, condaBaseURL+"/repository/conda-fromjson/linux-64/pkgs/"+condaPkgFile, pkgURL)

	w := fetchThrough(t, r, pkgURL)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	assert.Equal(t, "from-packages-bytes", w.Body.String())
}

// TestConda_Proxy_IndexDocuments_NotCachedAsImmutable pins the second half of #145:
// these documents are indexes, not packages. The immutable-artifact branch caches a
// path forever and never contacts upstream again, so a channel that gained a package
// would never be seen. repodata.json is the reference — it is re-read from upstream on
// every request — and the other index documents must be treated identically rather
// than to some TTL of their own.
func TestConda_Proxy_IndexDocuments_NotCachedAsImmutable(t *testing.T) {
	for _, indexName := range []string{
		"repodata.json",
		"current_repodata.json",
		"repodata_from_packages.json",
	} {
		t.Run(indexName, func(t *testing.T) {
			upstream, hits := countingUpstream(t, indexName, condaPkgFile)
			defer upstream.Close()

			r, _ := subpathProxy(t, "conda-fresh", upstream.URL)

			first := indexVersion(t, r, "conda-fresh", indexName, condaPkgFile)
			second := indexVersion(t, r, "conda-fresh", indexName, condaPkgFile)

			assert.NotEqual(t, first, second,
				"the channel changed between the two requests and the client was not told")
			assert.Equal(t, int64(2), hits.Load(),
				"an index must be re-read from upstream, not served from the immutable cache")
		})
	}
}

// TestConda_Proxy_CompressedIndexVariants_NotServed pins the decision on the
// compressed variants. repodata.json.bz2 has always answered 404 and conda clients
// fall back to the plain document; .zst gets the same answer, for the same reason —
// serving it would mean decompressing, rewriting and recompressing an index that can
// run to hundreds of megabytes on every request. Upstream must not be contacted at
// all, and nothing may be cached under the path.
func TestConda_Proxy_CompressedIndexVariants_NotServed(t *testing.T) {
	for name, want := range map[string]string{
		"repodata.json.bz2":               "repodata.json",
		"repodata.json.zst":               "repodata.json",
		"current_repodata.json.zst":       "current_repodata.json",
		"current_repodata.json.bz2":       "current_repodata.json",
		"repodata_from_packages.json.zst": "repodata_from_packages.json",
		"repodata.jlap":                   "repodata.json",
		"current_repodata.jlap":           "current_repodata.json",
	} {
		t.Run(name, func(t *testing.T) {
			upstream, hits := countingUpstream(t, "repodata.json", condaPkgFile)
			defer upstream.Close()

			r, _ := subpathProxy(t, "conda-compressed", upstream.URL)

			req := httptest.NewRequest(http.MethodGet,
				"/repository/conda-compressed/linux-64/"+name, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			assert.Equal(t, http.StatusNotFound, w.Code, "body: %s", w.Body.String())
			assert.Contains(t, w.Body.String(), "use "+want,
				"the 404 must name the document the client should ask for instead")
			assert.Equal(t, int64(0), hits.Load(),
				"a refused variant must not be fetched from upstream")
		})
	}
}

// TestConda_Proxy_PackageNamedLikeAnIndex_StillDownloads guards the refusal rule
// against overreach: only an index document at the root of a subdir is refused, and a
// package file that happens to sit under a directory of that name is still a download.
func TestConda_Proxy_PackageNamedLikeAnIndex_StillDownloads(t *testing.T) {
	upstream := subdirUpstream(t, "/linux-64/repodata.json",
		"pkgs/repodata.json.zst", "/linux-64/pkgs/repodata.json.zst", "not-an-index")
	defer upstream.Close()

	r, _ := subpathProxy(t, "conda-lookalike", upstream.URL)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/conda-lookalike/linux-64/pkgs/repodata.json.zst", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	assert.Equal(t, "not-an-index", w.Body.String())
}
