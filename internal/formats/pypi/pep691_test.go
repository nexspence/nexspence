package pypi_test

// PEP 691 regression tests (#191): modern pip asks for the JSON simple page
// (application/vnd.pypi.simple.v1+json). Forwarding that Accept upstream broke
// URL rewriting, group merging, and cache consistency, because the whole
// simple-page pipeline is built around the PEP 503 HTML representation. The
// proxy must always request text/html upstream regardless of the client's
// Accept header.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/group"
	"github.com/nexspence-oss/nexspence/internal/formats/pypi"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// pipAccept is what pip ≥ 22.2 sends for simple pages (PEP 691).
const pipAccept = "application/vnd.pypi.simple.v1+json, application/vnd.pypi.simple.v1+html;q=0.1, text/html;q=0.01"

// pep691Upstream models pypi.org content negotiation: JSON only when the
// client explicitly asks for it, HTML otherwise.
func pep691Upstream(t *testing.T, capturedAccept *string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*capturedAccept = r.Header.Get("Accept")
		if strings.Contains(*capturedAccept, "application/vnd.pypi.simple.v1+json") {
			w.Header().Set("Content-Type", "application/vnd.pypi.simple.v1+json")
			fmt.Fprint(w, `{"files":[{"filename":"requests-2.31.0-py3-none-any.whl","url":"https://files.pythonhosted.org/packages/ab/cd/requests-2.31.0-py3-none-any.whl"}],"name":"requests"}`)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!DOCTYPE html><html><body><a href="https://files.pythonhosted.org/packages/ab/cd/requests-2.31.0-py3-none-any.whl#sha256=deadbeef">requests-2.31.0-py3-none-any.whl</a></body></html>`)
	}))
}

func proxyRepo(name, remoteURL string) *domain.Repository {
	return &domain.Repository{
		ID:          name,
		Name:        name,
		Format:      "pypi",
		Type:        domain.TypeProxy,
		Online:      true,
		ProxyConfig: map[string]any{"remote_url": remoteURL},
	}
}

// TestPyPI_Proxy_PackageIndex_ForcesHTMLUpstream: a pip client asking for the
// JSON simple page must not make the proxy fetch JSON from upstream — the
// upstream request always asks for text/html, and the served page is the
// rewritten HTML representation.
func TestPyPI_Proxy_PackageIndex_ForcesHTMLUpstream(t *testing.T) {
	var capturedAccept string
	upstream := pep691Upstream(t, &capturedAccept)
	defer upstream.Close()

	r := setupExtra(proxyRepo("pypi-prx-691", upstream.URL))

	req := httptest.NewRequest(http.MethodGet, "/repository/pypi-prx-691/simple/requests/", nil)
	req.Header.Set("Accept", pipAccept)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "text/html", capturedAccept, "upstream fetch must always ask for the PEP 503 HTML page")
	body := w.Body.String()
	assert.NotContains(t, body, `"files"`, "client must get HTML, not the PEP 691 JSON page")
	assert.Contains(t, body, `href="http://localhost:8080/repository/pypi-prx-691/packages/ab/cd/requests-2.31.0-py3-none-any.whl#sha256=deadbeef"`,
		"file links must be rewritten to the proxy")
}

// TestPyPI_Proxy_SimpleIndex_ForcesHTMLUpstream: same guarantee for the root
// simple index (/simple/).
func TestPyPI_Proxy_SimpleIndex_ForcesHTMLUpstream(t *testing.T) {
	var capturedAccept string
	upstream := pep691Upstream(t, &capturedAccept)
	defer upstream.Close()

	r := setupExtra(proxyRepo("pypi-prx-691-idx", upstream.URL))

	req := httptest.NewRequest(http.MethodGet, "/repository/pypi-prx-691-idx/simple/", nil)
	req.Header.Set("Accept", pipAccept)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "text/html", capturedAccept, "upstream fetch must always ask for the PEP 503 HTML page")
	assert.NotContains(t, w.Body.String(), `"files"`)
}

// TestPyPI_Group_PipAccept_MergesProxyMember: end-to-end group reproduction
// from #191 — pip's Accept header must not empty out the merged group page.
// The proxy member fetches HTML upstream, so the anchor-based merge sees links.
func TestPyPI_Group_PipAccept_MergesProxyMember(t *testing.T) {
	var capturedAccept string
	upstream := pep691Upstream(t, &capturedAccept)
	defer upstream.Close()

	member := proxyRepo("pypi-prx-g691", upstream.URL)
	groupDef := &domain.Repository{
		ID:     "pypi-group-691",
		Name:   "pypi-group-691",
		Format: "pypi",
		Type:   domain.TypeGroup,
		Online: true,
		FormatConfig: map[string]any{
			"member_names": []interface{}{"pypi-prx-g691"},
		},
	}

	repoRepo := testutil.NewRepoRepo(member, groupDef)
	d := formats.Deps{
		Repos:      repoRepo,
		Blobs:      testutil.NewBlobStoreRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
		BaseURL:    "http://localhost:8080",
	}
	pypiH := pypi.New(d)
	groupH := group.New(d, map[string]formats.FormatHandler{"pypi": pypiH})

	r := gin.New()
	r.Any("/repository/:repoName/*path", func(c *gin.Context) {
		repo, _ := repoRepo.Get(c.Request.Context(), c.Param("repoName"))
		if repo != nil && repo.Type == domain.TypeGroup {
			groupH.ServeHTTP(c)
			return
		}
		pypiH.ServeHTTP(c)
	})

	req := httptest.NewRequest(http.MethodGet, "/repository/pypi-group-691/simple/requests/", nil)
	req.Header.Set("Accept", pipAccept)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "requests-2.31.0-py3-none-any.whl",
		"merged group page must not be empty for a pip client")
	assert.Contains(t, body, `/repository/pypi-group-691/packages/`,
		"file links must be re-rooted at the group")
	// The client's own request must be untouched by the member fan-out.
	assert.Equal(t, pipAccept, req.Header.Get("Accept"))
}
