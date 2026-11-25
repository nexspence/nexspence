package pypi_test

// #821: every URL the client is expected to follow must be root-relative.
// The server mints links from http.base_url, which is the server's own
// configuration — behind a reverse proxy it is not the host the client
// reached, so the link points somewhere unreachable. A root-relative href
// carries no host and resolves against the host the client already used.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/pypi"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// hrefAttrRe extracts the href attribute values of a page.
var hrefAttrRe = regexp.MustCompile(`href="([^"]*)"`)

// assertNoAbsoluteHrefs fails when the page carries an href with a host.
// A scheme or a "//host" form both mean the client is told where to go.
func assertNoAbsoluteHrefs(t *testing.T, body string) {
	t.Helper()
	for _, m := range hrefAttrRe.FindAllStringSubmatch(body, -1) {
		href := m[1]
		assert.False(t, strings.HasPrefix(href, "http://"),
			"href %q must be root-relative, not an absolute URL", href)
		assert.False(t, strings.HasPrefix(href, "https://"),
			"href %q must be root-relative, not an absolute URL", href)
		assert.False(t, strings.HasPrefix(href, "//"),
			"href %q must be root-relative, not a protocol-relative URL", href)
	}
}

// setupWithBaseURL builds a handler whose configured base URL is baseURL —
// deliberately not the host the test client reaches.
func setupWithBaseURL(repo *domain.Repository, baseURL string) *gin.Engine {
	d := formats.Deps{
		Repos:      testutil.NewRepoRepo(repo),
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    baseURL,
	}
	h := pypi.New(d)
	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) { h.ServeHTTP(c) })
	return r
}

// The proxy's per-package page must carry root-relative file links: pip
// resolves them against the host it reached, so no configuration can break them.
func TestPyPI_Proxy_PackagePage_HrefsAreRootRelative(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body><a href="https://files.pythonhosted.org/packages/aa/bb/flask-3.0.0-py3-none-any.whl#sha256=cafe">flask</a></body></html>`)
	}))
	defer upstream.Close()

	repo := &domain.Repository{
		ID: "py-rel", Name: "pypi-rel", Format: "pypi",
		Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": upstream.URL},
	}
	r := setupWithBaseURL(repo, "http://wrong.example:8080")

	req := httptest.NewRequest(http.MethodGet, "/repository/pypi-rel/simple/flask/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	body := w.Body.String()
	assert.Contains(t, body, `href="/repository/pypi-rel/packages/aa/bb/flask-3.0.0-py3-none-any.whl#sha256=cafe"`,
		"the file link must be root-relative and keep the #sha256 fragment")
	assert.NotContains(t, body, "files.pythonhosted.org")
	assert.NotContains(t, body, "wrong.example", "the configured base URL must not reach the client")
	assertNoAbsoluteHrefs(t, body)
}

// The hosted package page mints the same root-relative href.
func TestPyPI_Hosted_PackagePage_HrefIsRootRelative(t *testing.T) {
	repo := testutil.SimpleRepo("pypi-rel-hosted", "pypi")
	r := setupWithBaseURL(repo, "http://wrong.example:8080")

	require.Equal(t, http.StatusOK,
		upload(r, "pypi-rel-hosted", "flask", "3.0.0", "flask-3.0.0-py3-none-any.whl", "bytes"))

	req := httptest.NewRequest(http.MethodGet, "/repository/pypi-rel-hosted/simple/flask/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	body := w.Body.String()
	assert.Contains(t, body, `href="/repository/pypi-rel-hosted/packages/flask/flask-3.0.0-py3-none-any.whl`)
	assert.NotContains(t, body, "wrong.example", "the configured base URL must not reach the client")
	assertNoAbsoluteHrefs(t, body)
}

// The group's merged page re-roots the member link at the group, still
// without a host.
func TestPyPI_GroupIndex_HrefIsRootRelative(t *testing.T) {
	h := pypi.New(formats.Deps{BaseURL: "http://wrong.example:8080"})

	member := []byte(`<html><body><h1>Links for pkg</h1>
<a href="/repository/m1/packages/aa/pkg-1.0.whl#sha256=aaa">pkg-1.0.whl</a>
</body></html>`)

	body, _, err := h.MergeGroupIndex("py-group", "/simple/pkg", []formats.GroupIndexPart{
		{Member: "m1", Body: member},
	})
	require.NoError(t, err)
	out := string(body)

	assert.Contains(t, out, `href="/repository/py-group/packages/aa/pkg-1.0.whl#sha256=aaa"`,
		"the member link must be re-rooted at the group without a host")
	assert.NotContains(t, out, "wrong.example", "the configured base URL must not reach the client")
	assertNoAbsoluteHrefs(t, out)
}

// The case the bug is about: a handler whose configured base URL points
// somewhere else entirely still serves links that carry no host at all, so a
// client behind a reverse proxy can follow them.
func TestPyPI_PackagePage_CarriesNoHost_EvenWithForeignBaseURL(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body><a href="https://files.pythonhosted.org/packages/aa/bb/pkg-1.0.tar.gz">pkg</a></body></html>`)
	}))
	defer upstream.Close()

	proxy := &domain.Repository{
		ID: "py-host", Name: "pypi-host", Format: "pypi",
		Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": upstream.URL},
	}
	hosted := testutil.SimpleRepo("pypi-host-hosted", "pypi")

	for _, tc := range []struct {
		name   string
		repo   *domain.Repository
		target string
	}{
		{"proxy", proxy, "/repository/pypi-host/simple/pkg/"},
		{"hosted", hosted, "/repository/pypi-host-hosted/simple/flask/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := setupWithBaseURL(tc.repo, "http://wrong.example:8080")
			if tc.name == "hosted" {
				require.Equal(t, http.StatusOK,
					upload(r, "pypi-host-hosted", "flask", "3.0.0", "flask-3.0.0-py3-none-any.whl", "bytes"))
			}

			req := httptest.NewRequest(http.MethodGet, tc.target, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			body := w.Body.String()
			assert.NotContains(t, body, "wrong.example",
				"the configured base URL must not reach the client")
			assertNoAbsoluteHrefs(t, body)
			assert.Contains(t, body, `href="/repository/`, "at least one root-relative link must be present")
		})
	}
}
