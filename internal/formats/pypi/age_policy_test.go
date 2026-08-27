package pypi_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
)

// Minimum-package-age policy on a PyPI proxy (#323): files uploaded more
// recently than the configured age disappear from the simple page and are
// refused on direct download. Dates come from the upstream PEP 691 JSON page
// (the HTML page pip is served carries none); an upstream that cannot provide
// them leaves the policy unapplied (hybrid failure mode).

const (
	oldWhl   = "requests-2.31.0-py3-none-any.whl"
	youngWhl = "requests-2.32.0-py3-none-any.whl"
)

// agedPyPIUpstream negotiates like pypi.org: PEP 691 JSON (with upload-time)
// when asked for it, PEP 503 HTML otherwise. jsonCapable=false models an index
// that only speaks HTML. fileHits counts /packages/ downloads.
func agedPyPIUpstream(t *testing.T, jsonCapable bool) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var fileHits atomic.Int32
	now := time.Now().UTC()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/packages/") {
			fileHits.Add(1)
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write([]byte("wheel-bytes"))
			return
		}
		if jsonCapable && strings.Contains(r.Header.Get("Accept"), "application/vnd.pypi.simple.v1+json") {
			w.Header().Set("Content-Type", "application/vnd.pypi.simple.v1+json")
			fmt.Fprintf(w, `{"name":"requests","files":[
				{"filename":%q,"url":"https://files.pythonhosted.org/packages/ab/cd/%s","upload-time":%q},
				{"filename":%q,"url":"https://files.pythonhosted.org/packages/ab/ce/%s","upload-time":%q}
			]}`,
				oldWhl, oldWhl, now.Add(-90*24*time.Hour).Format(time.RFC3339),
				youngWhl, youngWhl, now.Add(-1*time.Hour).Format(time.RFC3339))
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<!DOCTYPE html><html><body>
<a href="https://files.pythonhosted.org/packages/ab/cd/%s#sha256=aa">%s</a><br/>
<a href="https://files.pythonhosted.org/packages/ab/ce/%s#sha256=bb">%s</a><br/>
</body></html>`, oldWhl, oldWhl, youngWhl, youngWhl)
	}))
	t.Cleanup(srv.Close)
	return srv, &fileHits
}

func agedPyPIProxy(upstreamURL string) *gin.Engine {
	repo := proxyRepo("pypi-aged", upstreamURL)
	repo.ProxyConfig["minimum_package_age"] = 7 * 24 * 3600
	return setupExtra(repo)
}

func TestPyPI_AgePolicy_SimplePageHidesYoungFile(t *testing.T) {
	upstream, _ := agedPyPIUpstream(t, true)
	r := agedPyPIProxy(upstream.URL)

	req := httptest.NewRequest(http.MethodGet, "/repository/pypi-aged/simple/requests/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	body := w.Body.String()
	assert.Contains(t, body, oldWhl)
	assert.NotContains(t, body, youngWhl,
		"an hour-old upload must be invisible behind a 7-day policy")
}

func TestPyPI_AgePolicy_YoungFileDownloadIs403(t *testing.T) {
	upstream, fileHits := agedPyPIUpstream(t, true)
	r := agedPyPIProxy(upstream.URL)

	req := httptest.NewRequest(http.MethodGet, "/repository/pypi-aged/packages/ab/ce/"+youngWhl, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code,
		"a direct URL must not bypass the simple-page filter: %d %s", w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "minimum package age")
	assert.Zero(t, fileHits.Load(), "the young file must never be fetched upstream")
}

func TestPyPI_AgePolicy_OldFileDownloadServes(t *testing.T) {
	upstream, _ := agedPyPIUpstream(t, true)
	r := agedPyPIProxy(upstream.URL)

	req := httptest.NewRequest(http.MethodGet, "/repository/pypi-aged/packages/ab/cd/"+oldWhl, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, "wheel-bytes", w.Body.String())
}

// Hybrid failure mode: an upstream that only speaks HTML carries no upload
// dates — the policy is skipped rather than hiding the whole package.
func TestPyPI_AgePolicy_HTMLOnlyUpstream_SkipsPolicy(t *testing.T) {
	upstream, _ := agedPyPIUpstream(t, false)
	r := agedPyPIProxy(upstream.URL)

	req := httptest.NewRequest(http.MethodGet, "/repository/pypi-aged/simple/requests/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), youngWhl,
		"no dates available → the page is served as-is")

	req = httptest.NewRequest(http.MethodGet, "/repository/pypi-aged/packages/ab/ce/"+youngWhl, nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code, "no dates → the download gate opens too")
}

// The pipeline invariant from #191 must survive the policy: the SERVED page
// stays the rewritten HTML representation even while dates are fetched as JSON.
func TestPyPI_AgePolicy_ServedPageStaysHTML(t *testing.T) {
	upstream, _ := agedPyPIUpstream(t, true)
	r := agedPyPIProxy(upstream.URL)

	req := httptest.NewRequest(http.MethodGet, "/repository/pypi-aged/simple/requests/", nil)
	req.Header.Set("Accept", pipAccept)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.NotContains(t, body, `"files"`, "client must get HTML, not the PEP 691 JSON page")
	assert.Contains(t, body, `href="http://localhost:8080/repository/pypi-aged/packages/ab/cd/`+oldWhl,
		"file links must still be rewritten to the proxy")
}

// A proxy without the option keeps today's behavior.
func TestPyPI_AgePolicy_DisabledByDefault(t *testing.T) {
	upstream, _ := agedPyPIUpstream(t, true)
	r := setupExtra(proxyRepo("pypi-noage", upstream.URL))

	req := httptest.NewRequest(http.MethodGet, "/repository/pypi-noage/simple/requests/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), youngWhl)
}

var _ = domain.TypeProxy // keep the import when helpers move
