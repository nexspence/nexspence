package helm_test

// #821: every URL the client is expected to follow must be root-relative.
// The server mints links from http.base_url, which is the server's own
// configuration — behind a reverse proxy it is not the host the client
// reached, so the link points somewhere unreachable. A root-relative path
// carries no host and resolves against the host the client already used.
//
// Helm resolves an index.yaml `urls:` entry as a URL reference against the
// repository URL (helm.sh/helm/v3/pkg/repo.ResolveReferenceURL), so a path
// without a host is enough — verified against helm 4.1.4: `helm pull` fetches
// a chart whose entry carries a root-relative urls path.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/helm"
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
	h := helm.New(d)
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

// indexYAML serves the repository's index.yaml and returns the body.
func indexYAML(t *testing.T, r *gin.Engine, repoName string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/repository/"+repoName+"/index.yaml", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	return w.Body.String()
}

// The hosted index names each chart with a root-relative download URL.
func TestHelm_HostedIndex_ChartURLIsRootRelative(t *testing.T) {
	repo := testutil.SimpleRepo("helm-rel-hosted", "helm")
	r := setupWithBaseURL(repo, wrongBaseURL)

	require.Equal(t, http.StatusCreated,
		uploadMultipart(r, "helm-rel-hosted", "widget-1.2.3.tgz", "widget"))

	body := indexYAML(t, r, "helm-rel-hosted")
	assert.Contains(t, body, "/repository/helm-rel-hosted/widget-1.2.3.tgz",
		"the chart URL must be root-relative")
	assertNoAbsoluteURL(t, body)
}

// The proxied index is rewritten the same way: the upstream's own URL becomes
// a path under this repository, with no host.
func TestHelm_ProxyIndex_ChartURLIsRootRelative(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		fmt.Fprint(w, `apiVersion: v1
entries:
  widget:
    - name: widget
      version: 1.2.3
      urls:
        - https://charts.example.com/widget-1.2.3.tgz
generated: "2024-01-01T00:00:00Z"
`)
	}))
	defer upstream.Close()

	repo := &domain.Repository{
		ID: "helm-rel-proxy", Name: "helm-rel-proxy", Format: "helm",
		Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": upstream.URL},
	}
	r := setupWithBaseURL(repo, wrongBaseURL)

	body := indexYAML(t, r, "helm-rel-proxy")
	assert.Contains(t, body, "/repository/helm-rel-proxy/widget-1.2.3.tgz",
		"the rewritten chart URL must be root-relative")
	assert.NotContains(t, body, "charts.example.com",
		"the upstream URL must not be handed to the client")
	assertNoAbsoluteURL(t, body)
}

// The served index must not depend on the request's Host header. A link built
// from the request's host would be the cache-poisoning vector this fix exists
// to avoid — and it would be invisible: every other test sends the usual
// host, so such a change would still pass them all. The same request served
// under a foreign host must return the very same bytes.
func TestHelm_Index_DoesNotDependOnRequestHost(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		fmt.Fprint(w, `apiVersion: v1
entries:
  widget:
    - name: widget
      version: 1.2.3
      urls:
        - https://charts.example.com/widget-1.2.3.tgz
generated: "2024-01-01T00:00:00Z"
`)
	}))
	defer upstream.Close()

	repo := &domain.Repository{
		ID: "helm-rel-hostdep", Name: "helm-rel-hostdep", Format: "helm",
		Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": upstream.URL},
	}
	r := setupWithBaseURL(repo, "http://localhost:8080")

	serve := func(host string) []byte {
		req := httptest.NewRequest(http.MethodGet, "/repository/helm-rel-hostdep/index.yaml", nil)
		req.Host = host
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		return w.Body.Bytes()
	}

	normal := serve("localhost")
	foreign := serve("myrepo.ru")

	assert.Contains(t, string(normal), "urls:",
		"the comparison must not be vacuous: the index carries links")
	assert.Equal(t, normal, foreign,
		"the served index must not depend on the request's Host header")
}

// The chart URL of the hosted index is the one helm follows: it must resolve
// against the repository URL, so it has to keep the repository's own path.
func TestHelm_HostedIndex_ChartURLKeepsRepositoryPath(t *testing.T) {
	repo := testutil.SimpleRepo("helm-rel-path", "helm")
	r := setupWithBaseURL(repo, wrongBaseURL)

	require.Equal(t, http.StatusCreated,
		uploadMultipart(r, "helm-rel-path", "widget-1.2.3.tgz", "widget"))

	body := indexYAML(t, r, "helm-rel-path")
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, "- /") {
			assert.True(t, strings.HasPrefix(strings.TrimSpace(line), "- /repository/helm-rel-path/"),
				"the chart URL must be a path under the repository, got %q", line)
		}
	}
}
