package helm_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/helm"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

const testBaseURL = "http://localhost:8080"

// setupProxy builds a helm proxy repository pointing at remoteURL and returns the
// engine plus the in-memory component repo so tests can assert what got cached.
func setupProxy(t *testing.T, repoName, remoteURL string) (*gin.Engine, *testutil.ComponentRepo) {
	t.Helper()
	return setupProxyWithIndexTTL(t, repoName, remoteURL, 5*time.Minute)
}

// setupProxyWithIndexTTL is setupProxy with helm.index_cache_ttl spelled out; 0
// makes every chart lookup fetch the upstream index.
func setupProxyWithIndexTTL(t *testing.T, repoName, remoteURL string, indexTTL time.Duration,
) (*gin.Engine, *testutil.ComponentRepo) {
	t.Helper()
	comps := testutil.NewComponentRepo()
	repo := &domain.Repository{
		ID: repoName, Name: repoName, Format: "helm",
		Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": remoteURL},
	}
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: comps,
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    testBaseURL,

		HelmIndexCacheTTL: indexTTL,
	}
	h := helm.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	return r, comps
}

// setupGroupMemberProxy is setupProxy with GroupMemberKey set, as the group
// fan-out does when it asks a member for a chart.
func setupGroupMemberProxy(t *testing.T, repoName, remoteURL string) (*gin.Engine, *testutil.ComponentRepo) {
	t.Helper()
	comps := testutil.NewComponentRepo()
	repo := &domain.Repository{
		ID: repoName, Name: repoName, Format: "helm",
		Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": remoteURL},
	}
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: comps,
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    testBaseURL,

		HelmIndexCacheTTL: 5 * time.Minute,
	}
	h := helm.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) {
		c.Set(formats.GroupMemberKey, true)
		h.ServeHTTP(c)
	})
	return r, comps
}

// nestedUpstream serves an index.yaml whose single entry URL is entryURL, plus the
// chart tarball at tgzPath.
func nestedUpstream(t *testing.T, entryURL, tgzPath, body string) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.yaml":
			url := entryURL
			// "%s" in entryURL is filled with the server's own base URL so tests can
			// express absolute-under-remote entries.
			if strings.Contains(url, "%s") {
				url = strings.ReplaceAll(url, "%s", srv.URL)
			}
			w.Header().Set("Content-Type", "application/yaml")
			_, _ = w.Write([]byte("apiVersion: v1\n" +
				"entries:\n" +
				"  ingress-nginx:\n" +
				"  - name: ingress-nginx\n" +
				"    version: \"4.11.2\"\n" +
				"    urls:\n" +
				"    - " + url + "\n" +
				"generated: \"2024-01-01T00:00:00Z\"\n"))
		case tgzPath:
			w.Header().Set("Content-Type", "application/x-tar")
			_, _ = w.Write([]byte(body))
		default:
			http.NotFound(w, r)
		}
	}))
	return srv
}

// firstChartURL fetches index.yaml through the proxy and returns the single
// rewritten chart URL the helm client would follow.
func firstChartURL(t *testing.T, r *gin.Engine, repoName string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/repository/"+repoName+"/index.yaml", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "index.yaml body: %s", w.Body.String())

	var index struct {
		Entries map[string][]struct {
			URLs []string `yaml:"urls"`
		} `yaml:"entries"`
	}
	require.NoError(t, yaml.Unmarshal(w.Body.Bytes(), &index))
	require.Len(t, index.Entries, 1)
	for _, versions := range index.Entries {
		require.Len(t, versions, 1)
		require.Len(t, versions[0].URLs, 1)
		return versions[0].URLs[0]
	}
	return ""
}

// TestHelm_Proxy_NestedChartURL_RoundTrip is the reported case (#139): the upstream
// index points at charts/<name>-<version>.tgz, a path below the repository root.
// The download path is taken from the rewritten index, not hand-built, so this
// proves the whole round trip closes.
func TestHelm_Proxy_NestedChartURL_RoundTrip(t *testing.T) {
	upstream := nestedUpstream(t, "charts/ingress-nginx-4.11.2.tgz",
		"/charts/ingress-nginx-4.11.2.tgz", "nested-chart-bytes")
	defer upstream.Close()

	r, _ := setupProxy(t, "helm-nested", upstream.URL)

	chartURL := firstChartURL(t, r, "helm-nested")
	assert.Equal(t, testBaseURL+"/repository/helm-nested/charts/ingress-nginx-4.11.2.tgz", chartURL,
		"the upstream subdirectory must survive the rewrite")

	req := httptest.NewRequest(http.MethodGet, strings.TrimPrefix(chartURL, testBaseURL), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	assert.Equal(t, "nested-chart-bytes", w.Body.String())
}

// TestHelm_Proxy_FlatChartURL_RoundTrip is the regression guard: a flat upstream
// (no subdirectory) must keep working exactly as before.
func TestHelm_Proxy_FlatChartURL_RoundTrip(t *testing.T) {
	upstream := nestedUpstream(t, "ingress-nginx-4.11.2.tgz",
		"/ingress-nginx-4.11.2.tgz", "flat-chart-bytes")
	defer upstream.Close()

	r, _ := setupProxy(t, "helm-flat", upstream.URL)

	chartURL := firstChartURL(t, r, "helm-flat")
	assert.Equal(t, testBaseURL+"/repository/helm-flat/ingress-nginx-4.11.2.tgz", chartURL)

	req := httptest.NewRequest(http.MethodGet, strings.TrimPrefix(chartURL, testBaseURL), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	assert.Equal(t, "flat-chart-bytes", w.Body.String())
}

// TestHelm_Proxy_AbsoluteURLUnderRemote_RoundTrip covers an index that spells its
// URLs out in full against the configured remote: the remote prefix is stripped and
// the remainder — subdirectory included — becomes the proxy path.
func TestHelm_Proxy_AbsoluteURLUnderRemote_RoundTrip(t *testing.T) {
	upstream := nestedUpstream(t, "%s/charts/ingress-nginx-4.11.2.tgz",
		"/charts/ingress-nginx-4.11.2.tgz", "absolute-chart-bytes")
	defer upstream.Close()

	r, _ := setupProxy(t, "helm-abs", upstream.URL)

	chartURL := firstChartURL(t, r, "helm-abs")
	assert.Equal(t, testBaseURL+"/repository/helm-abs/charts/ingress-nginx-4.11.2.tgz", chartURL)

	req := httptest.NewRequest(http.MethodGet, strings.TrimPrefix(chartURL, testBaseURL), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	assert.Equal(t, "absolute-chart-bytes", w.Body.String())
}

// TestHelm_Proxy_AbsoluteURLForeignHost_RoundTrip covers charts published
// elsewhere (typically GitHub releases). The index URL is rewritten onto this
// proxy by basename; the GET fetches the original host and caches the tarball.
func TestHelm_Proxy_AbsoluteURLForeignHost_RoundTrip(t *testing.T) {
	releases := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/o/r/releases/download/v1/widget-1.0.0.tgz" {
			w.Header().Set("Content-Type", "application/x-tar")
			_, _ = w.Write([]byte("github-chart-bytes"))
			return
		}
		http.NotFound(w, r)
	}))
	defer releases.Close()

	foreign := releases.URL + "/o/r/releases/download/v1/widget-1.0.0.tgz"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/index.yaml" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write([]byte("apiVersion: v1\n" +
			"entries:\n" +
			"  widget:\n" +
			"  - name: widget\n" +
			"    version: \"1.0.0\"\n" +
			"    urls:\n" +
			"    - " + foreign + "\n" +
			"generated: \"2024-01-01T00:00:00Z\"\n"))
	}))
	defer upstream.Close()

	r, comps := setupProxy(t, "helm-foreign", upstream.URL)

	chartURL := firstChartURL(t, r, "helm-foreign")
	assert.Equal(t, testBaseURL+"/repository/helm-foreign/widget-1.0.0.tgz", chartURL)

	req := httptest.NewRequest(http.MethodGet, strings.TrimPrefix(chartURL, testBaseURL), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	assert.Equal(t, "github-chart-bytes", w.Body.String())

	page, err := comps.List(t.Context(), "helm-foreign", 100, 0)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assert.Equal(t, "widget", page.Items[0].Name)
	assert.Equal(t, "1.0.0", page.Items[0].Version)
}

// TestHelm_Proxy_ForeignHostNonCanonicalFilename_RoundTrip: an off-host origin is
// free to be named anything ("widget.tgz", "widget-v1.0.0.tgz" for version
// "1.0.0"). The GET recovers the origin by the rewritten local path, so the
// index rewrite mints the entry's own name — proxying the origin's basename
// instead collided with any other widget.tgz and filed version 0.0.0.
func TestHelm_Proxy_ForeignHostNonCanonicalFilename_RoundTrip(t *testing.T) {
	for _, tc := range []struct{ name, originFile string }{
		{"no version in the file name", "widget.tgz"},
		{"v-prefixed version in the file name", "widget-v1.0.0.tgz"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			releases := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/o/r/releases/download/v1.0.0/"+tc.originFile {
					w.Header().Set("Content-Type", "application/x-tar")
					_, _ = w.Write([]byte("github-chart-bytes"))
					return
				}
				http.NotFound(w, r)
			}))
			defer releases.Close()

			foreign := releases.URL + "/o/r/releases/download/v1.0.0/" + tc.originFile
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/index.yaml" {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/yaml")
				_, _ = w.Write([]byte("apiVersion: v1\n" +
					"entries:\n" +
					"  widget:\n" +
					"  - name: widget\n" +
					"    version: \"1.0.0\"\n" +
					"    urls:\n" +
					"    - " + foreign + "\n"))
			}))
			defer upstream.Close()

			r, comps := setupProxy(t, "helm-noncanon", upstream.URL)

			chartURL := firstChartURL(t, r, "helm-noncanon")
			assert.Equal(t, testBaseURL+"/repository/helm-noncanon/widget-1.0.0.tgz", chartURL)

			req := httptest.NewRequest(http.MethodGet, strings.TrimPrefix(chartURL, testBaseURL), nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
			assert.Equal(t, "github-chart-bytes", w.Body.String())

			page, err := comps.List(t.Context(), "helm-noncanon", 100, 0)
			require.NoError(t, err)
			require.Len(t, page.Items, 1)
			assert.Equal(t, "widget", page.Items[0].Name)
			assert.Equal(t, "1.0.0", page.Items[0].Version)
		})
	}
}

// TestHelm_Proxy_SameHostNonCanonicalFilename_RoundTrip: a same-host index URL
// keeps its original path (charts/widget.tgz, ingress-nginx-v4.11.2.tgz). The
// origin lookup is keyed by that rewritten path, not by splitChartFilename, so
// neither shape 404s.
func TestHelm_Proxy_SameHostNonCanonicalFilename_RoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name, chart, version, entryURL, tgzPath string
	}{
		{"subdirectory basename without version", "widget", "1.0.0",
			"charts/widget.tgz", "/charts/widget.tgz"},
		{"v-prefixed version in the file name", "ingress-nginx", "4.11.2",
			"ingress-nginx-v4.11.2.tgz", "/ingress-nginx-v4.11.2.tgz"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/index.yaml":
					w.Header().Set("Content-Type", "application/yaml")
					_, _ = w.Write([]byte("apiVersion: v1\n" +
						"entries:\n" +
						"  " + tc.chart + ":\n" +
						"  - name: " + tc.chart + "\n" +
						"    version: \"" + tc.version + "\"\n" +
						"    urls:\n" +
						"    - " + tc.entryURL + "\n"))
				case tc.tgzPath:
					w.Header().Set("Content-Type", "application/x-tar")
					_, _ = w.Write([]byte("samehost-chart-bytes"))
				default:
					http.NotFound(w, r)
				}
			}))
			defer upstream.Close()

			r, comps := setupProxy(t, "helm-samehost", upstream.URL)

			chartURL := firstChartURL(t, r, "helm-samehost")
			assert.Equal(t, testBaseURL+"/repository/helm-samehost"+tc.tgzPath, chartURL)

			req := httptest.NewRequest(http.MethodGet, strings.TrimPrefix(chartURL, testBaseURL), nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
			assert.Equal(t, "samehost-chart-bytes", w.Body.String())

			page, err := comps.List(t.Context(), "helm-samehost", 100, 0)
			require.NoError(t, err)
			require.Len(t, page.Items, 1)
			assert.Equal(t, tc.chart, page.Items[0].Name)
			assert.Equal(t, tc.version, page.Items[0].Version)
		})
	}
}

// TestHelm_Proxy_IndexCacheTTL: the origin lookup runs on every uncached tarball
// GET, so without a cache the first pull through a group of N proxy members costs
// N index downloads — Bitnami's index is tens of megabytes. helm.index_cache_ttl
// is how long a fetched index answers those lookups, and 0 turns it off.
func TestHelm_Proxy_IndexCacheTTL(t *testing.T) {
	pulls := []string{"widget-1.0.0.tgz", "gadget-2.0.0.tgz", "cilium-1.16.0.tgz"}

	for _, tc := range []struct {
		name     string
		ttl      time.Duration
		wantHits int
		hitsWhy  string
		repoName string
	}{
		{"a live TTL reuses the fetched index", 5 * time.Minute, 1,
			"the index must be reused across chart lookups", "helm-cache"},
		{"zero TTL fetches per lookup", 0, len(pulls),
			"caching off means one index fetch per chart lookup", "helm-nocache"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var indexHits int
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/index.yaml":
					indexHits++
					w.Header().Set("Content-Type", "application/yaml")
					_, _ = w.Write([]byte("apiVersion: v1\n" +
						"entries:\n" +
						"  widget:\n" +
						"  - name: widget\n" +
						"    version: \"1.0.0\"\n" +
						"    urls:\n" +
						"    - widget-1.0.0.tgz\n" +
						"  gadget:\n" +
						"  - name: gadget\n" +
						"    version: \"2.0.0\"\n" +
						"    urls:\n" +
						"    - gadget-2.0.0.tgz\n"))
				case "/widget-1.0.0.tgz", "/gadget-2.0.0.tgz":
					w.Header().Set("Content-Type", "application/x-tar")
					_, _ = w.Write([]byte("chart-bytes"))
				default:
					http.NotFound(w, r)
				}
			}))
			defer upstream.Close()

			r, _ := setupProxyWithIndexTTL(t, tc.repoName, upstream.URL, tc.ttl)
			for _, file := range pulls {
				req := httptest.NewRequest(http.MethodGet, "/repository/"+tc.repoName+"/"+file, nil)
				r.ServeHTTP(httptest.NewRecorder(), req)
			}

			assert.Equal(t, tc.wantHits, indexHits, tc.hitsWhy)
		})
	}
}

// prefixedUpstream serves a repository rooted at /charts-repo, i.e. a remote whose
// URL carries a path prefix. index.yaml lists two entries: one inside the prefixed
// subtree and one outside it.
func prefixedUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/charts-repo/index.yaml":
			w.Header().Set("Content-Type", "application/yaml")
			_, _ = w.Write([]byte("apiVersion: v1\n" +
				"entries:\n" +
				"  inside:\n" +
				"  - name: inside\n" +
				"    version: \"1.0.0\"\n" +
				"    urls:\n" +
				"    - " + srv.URL + "/charts-repo/charts/inside-1.0.0.tgz\n" +
				"  outside:\n" +
				"  - name: outside\n" +
				"    version: \"1.0.0\"\n" +
				"    urls:\n" +
				"    - " + srv.URL + "/other-repo/outside-1.0.0.tgz\n" +
				"generated: \"2024-01-01T00:00:00Z\"\n"))
		case "/charts-repo/charts/inside-1.0.0.tgz":
			w.Header().Set("Content-Type", "application/x-tar")
			_, _ = w.Write([]byte("inside-chart-bytes"))
		case "/other-repo/outside-1.0.0.tgz":
			w.Header().Set("Content-Type", "application/x-tar")
			_, _ = w.Write([]byte("outside-chart-bytes"))
		default:
			http.NotFound(w, r)
		}
	}))
	return srv
}

// TestHelm_Proxy_RemoteWithPathPrefix covers a remote_url that has its own path
// prefix: an entry inside that subtree is proxied with the prefix stripped, and one
// outside it is still pulled through this proxy (basename path, origin URL fetch).
func TestHelm_Proxy_RemoteWithPathPrefix(t *testing.T) {
	upstream := prefixedUpstream(t)
	defer upstream.Close()

	r, _ := setupProxy(t, "helm-prefix", upstream.URL+"/charts-repo")

	req := httptest.NewRequest(http.MethodGet, "/repository/helm-prefix/index.yaml", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	var index struct {
		Entries map[string][]struct {
			URLs []string `yaml:"urls"`
		} `yaml:"entries"`
	}
	require.NoError(t, yaml.Unmarshal(w.Body.Bytes(), &index))

	inside := index.Entries["inside"][0].URLs[0]
	assert.Equal(t, testBaseURL+"/repository/helm-prefix/charts/inside-1.0.0.tgz", inside,
		"the remote's own path prefix must be stripped, the rest kept")
	outside := index.Entries["outside"][0].URLs[0]
	assert.Equal(t, testBaseURL+"/repository/helm-prefix/outside-1.0.0.tgz", outside,
		"a URL outside the proxied subtree is still fetched through this proxy")

	req = httptest.NewRequest(http.MethodGet, strings.TrimPrefix(inside, testBaseURL), nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	assert.Equal(t, "inside-chart-bytes", w.Body.String())

	req = httptest.NewRequest(http.MethodGet, strings.TrimPrefix(outside, testBaseURL), nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	assert.Equal(t, "outside-chart-bytes", w.Body.String())
}

// TestHelm_Proxy_ProvenanceFile_SharesChartCoords verifies the ".prov" signature
// Helm fetches alongside a chart is filed under the chart's own coordinates rather
// than registering a junk component of its own.
func TestHelm_Proxy_ProvenanceFile_SharesChartCoords(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/charts/ingress-nginx-4.11.2.tgz":
			_, _ = w.Write([]byte("chart"))
		case "/charts/ingress-nginx-4.11.2.tgz.prov":
			_, _ = w.Write([]byte("signature"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	r, comps := setupProxy(t, "helm-prov", upstream.URL)

	for _, p := range []string{
		"/repository/helm-prov/charts/ingress-nginx-4.11.2.tgz",
		"/repository/helm-prov/charts/ingress-nginx-4.11.2.tgz.prov",
	} {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code, "%s body: %s", p, w.Body.String())
	}

	page, err := comps.List(t.Context(), "helm-prov", 100, 0)
	require.NoError(t, err)
	require.Len(t, page.Items, 1, "the provenance file must not register its own component")
	assert.Equal(t, "ingress-nginx", page.Items[0].Name)
	assert.Equal(t, "4.11.2", page.Items[0].Version)
}

// TestHelm_Proxy_NestedPath_NoTraversal is a standing guard on the pre-existing
// normPath: it resolves ".." before the request path is forwarded upstream, so a
// traversal attempt lands back inside the remote subtree. This held before the
// nested-path fix too — ServeGET already received the full request path — and the
// test exists to keep it that way now that nested paths are routinely in play.
func TestHelm_Proxy_NestedPath_NoTraversal(t *testing.T) {
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		http.NotFound(w, r)
	}))
	defer upstream.Close()

	r, _ := setupProxy(t, "helm-trav", upstream.URL+"/base")

	req := httptest.NewRequest(http.MethodGet,
		"/repository/helm-trav/charts/../../../../etc/passwd", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, "/base/etc/passwd", gotPath,
		"the remote base prefix must survive a traversal attempt")
}

// TestHelm_Proxy_NestedChart_CachedCoords verifies the cached component carries the
// chart name and version parsed from the filename, not the request path.
func TestHelm_Proxy_NestedChart_CachedCoords(t *testing.T) {
	upstream := nestedUpstream(t, "charts/ingress-nginx-4.11.2.tgz",
		"/charts/ingress-nginx-4.11.2.tgz", "nested-chart-bytes")
	defer upstream.Close()

	r, comps := setupProxy(t, "helm-coords", upstream.URL)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/helm-coords/charts/ingress-nginx-4.11.2.tgz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())

	page, err := comps.List(req.Context(), "helm-coords", 100, 0)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	assert.Equal(t, "ingress-nginx", page.Items[0].Name)
	assert.Equal(t, "4.11.2", page.Items[0].Version)
}

// TestHelm_Proxy_UnknownChartInIndexIsNotFound: a group member whose readable
// index does not list this chart must 404 without asking upstream for the
// tarball (Bitnami's S3 would 403, which used to stop group fan-out).
func TestHelm_Proxy_UnknownChartInIndexIsNotFound(t *testing.T) {
	hitTgz := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.yaml":
			w.Header().Set("Content-Type", "application/yaml")
			_, _ = w.Write([]byte("apiVersion: v1\nentries:\n  redis:\n    - name: redis\n      version: \"1.0.0\"\n      urls:\n        - redis-1.0.0.tgz\n"))
		default:
			hitTgz = true
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`<Error><Code>AccessDenied</Code></Error>`))
		}
	}))
	defer upstream.Close()

	r, _ := setupGroupMemberProxy(t, "helm-bitnami", upstream.URL)
	req := httptest.NewRequest(http.MethodGet, "/repository/helm-bitnami/cilium-1.16.0.tgz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
	assert.False(t, hitTgz, "must not fetch a chart the index does not list")
}

// TestHelm_Proxy_UnlistedChartDirectForwards: addressed directly, an unlisted
// chart is forwarded as a path. Some remotes serve versions the index omits
// (trimmed catalogs, Non-SemVer names); a 404 here would hide them.
func TestHelm_Proxy_UnlistedChartDirectForwards(t *testing.T) {
	hitTgz := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.yaml":
			w.Header().Set("Content-Type", "application/yaml")
			_, _ = w.Write([]byte("apiVersion: v1\nentries:\n  redis:\n    - name: redis\n      version: \"1.0.0\"\n      urls:\n        - redis-1.0.0.tgz\n"))
		case "/cilium-1.16.0.tgz":
			hitTgz = true
			w.Header().Set("Content-Type", "application/x-tar")
			_, _ = w.Write([]byte("unlisted-but-there"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	r, _ := setupProxy(t, "helm-direct", upstream.URL)
	req := httptest.NewRequest(http.MethodGet, "/repository/helm-direct/cilium-1.16.0.tgz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.True(t, hitTgz, "direct request must path-forward an unlisted chart")
	assert.Equal(t, "unlisted-but-there", w.Body.String())
}

// TestHelm_Proxy_UpstreamForbiddenStaysForbidden: a 403 is mapped to a miss only
// while a group fans out (see TestGroupMerge_HelmTarballSkipsUnreadableMember).
// Addressed directly, the proxy reports it as it is — a credential upstream
// rejects must not read as "no such chart".
func TestHelm_Proxy_UpstreamForbiddenStaysForbidden(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`<Error><Code>AccessDenied</Code></Error>`))
	}))
	defer upstream.Close()

	r, _ := setupProxy(t, "helm-denied", upstream.URL)
	req := httptest.NewRequest(http.MethodGet, "/repository/helm-denied/cilium-1.16.0.tgz", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
}
