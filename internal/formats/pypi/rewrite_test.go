package pypi_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats/pypi"
)

func TestRewriteSimplePage_Hrefs(t *testing.T) {
	// #98: proxied simple pages carry absolute files.pythonhosted.org links,
	// so pip downloads wheels directly from upstream, bypassing the cache.
	in := []byte(`<html><body>
<a href="https://files.pythonhosted.org/packages/ab/cd/xyz/requests-2.31.0-py3-none-any.whl#sha256=deadbeef">requests-2.31.0-py3-none-any.whl</a>
<a href="https://files.pythonhosted.org/packages/ef/12/abc/requests-2.31.0.tar.gz">requests-2.31.0.tar.gz</a>
</body></html>`)
	out := string(pypi.RewriteSimplePage(in, "http://localhost:8080/repository/pypi-proxy"))

	assert.Contains(t, out,
		`href="http://localhost:8080/repository/pypi-proxy/packages/ab/cd/xyz/requests-2.31.0-py3-none-any.whl#sha256=deadbeef"`,
		"path tail and #sha256 fragment must be preserved")
	assert.Contains(t, out,
		`href="http://localhost:8080/repository/pypi-proxy/packages/ef/12/abc/requests-2.31.0.tar.gz"`)
	assert.NotContains(t, out, "files.pythonhosted.org")
}

func TestRewriteSimplePage_LeavesOtherHrefsAlone(t *testing.T) {
	in := []byte(`<a href="../relative/thing">x</a> <a href="https://example.com/no-pkg/file.whl">y</a>`)
	out := string(pypi.RewriteSimplePage(in, "http://local/repository/p"))
	assert.Equal(t, string(in), out, "relative hrefs and URLs without /packages/ stay untouched")
}

func TestPyPI_Proxy_SimplePage_RewritesLinks(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body><a href="https://files.pythonhosted.org/packages/aa/bb/flask-3.0.0-py3-none-any.whl#sha256=cafe">flask</a></body></html>`)
	}))
	defer upstream.Close()

	repo := &domain.Repository{
		ID: "py-rw", Name: "pypi-rw", Format: "pypi",
		Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": upstream.URL},
	}
	r := setup(repo) // BaseURL: http://localhost:8080

	req := httptest.NewRequest(http.MethodGet, "/repository/pypi-rw/simple/flask/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "http://localhost:8080/repository/pypi-rw/packages/aa/bb/flask-3.0.0-py3-none-any.whl#sha256=cafe")
	assert.NotContains(t, body, "files.pythonhosted.org")
}
