package helm_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
	}
	h := helm.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
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

// TestHelm_Proxy_AbsoluteURLForeignHost_LeftAlone covers charts published elsewhere
// (typically GitHub releases). We cannot express those as a path under this proxy,
// so the URL must survive untouched and the client fetches it directly.
func TestHelm_Proxy_AbsoluteURLForeignHost_LeftAlone(t *testing.T) {
	const foreign = "https://github.com/kubernetes/ingress-nginx/releases/download/helm-chart-4.11.2/ingress-nginx-4.11.2.tgz"
	upstream := nestedUpstream(t, foreign, "/unused.tgz", "unused")
	defer upstream.Close()

	r, _ := setupProxy(t, "helm-foreign", upstream.URL)

	assert.Equal(t, foreign, firstChartURL(t, r, "helm-foreign"),
		"a chart on another host cannot be proxied and must not be rewritten")
}

// TestHelm_Proxy_NestedPath_NoTraversal guards the nested download path against
// escaping the configured remote: normPath resolves ".." before the path is ever
// forwarded upstream, so a traversal attempt lands back inside the remote subtree.
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
