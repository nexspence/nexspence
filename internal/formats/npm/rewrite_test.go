package npm_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats/npm"
)

func TestRewritePackument_TarballURLs(t *testing.T) {
	// #98: proxied packuments must point dist.tarball at this proxy, not
	// the upstream registry, or npm installs bypass the cache entirely.
	in := []byte(`{
		"name": "lodash",
		"dist-tags": {"latest": "4.17.21"},
		"versions": {
			"4.17.21": {
				"name": "lodash",
				"dist": {
					"shasum": "abc",
					"tarball": "https://registry.npmjs.org/lodash/-/lodash-4.17.21.tgz"
				}
			},
			"4.17.20": {
				"dist": {"tarball": "https://registry.npmjs.org/lodash/-/lodash-4.17.20.tgz"}
			}
		}
	}`)
	out := string(npm.RewritePackument(in, "http://localhost:8080/repository/npm-proxy"))

	assert.Contains(t, out, `"tarball":"http://localhost:8080/repository/npm-proxy/lodash/-/lodash-4.17.21.tgz"`)
	assert.Contains(t, out, "lodash-4.17.20.tgz")
	assert.NotContains(t, out, "registry.npmjs.org")
	// Non-dist fields survive the round-trip.
	assert.Contains(t, out, `"shasum":"abc"`)
	assert.Contains(t, out, `"dist-tags"`)
}

func TestRewritePackument_ScopedPackage(t *testing.T) {
	in := []byte(`{"versions":{"1.0.0":{"dist":{"tarball":"https://registry.npmjs.org/@babel/core/-/core-1.0.0.tgz"}}}}`)
	out := string(npm.RewritePackument(in, "http://localhost:8080/repository/npm-proxy"))
	assert.Contains(t, out, "http://localhost:8080/repository/npm-proxy/@babel/core/-/core-1.0.0.tgz")
}

func TestRewritePackument_MalformedBodyUnchanged(t *testing.T) {
	in := []byte("not json at all")
	out := npm.RewritePackument(in, "http://x")
	assert.Equal(t, in, out)
}

func TestNPM_Proxy_Packument_RewritesTarballs(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"name":"left-pad","versions":{"1.3.0":{"dist":{"tarball":"%s/left-pad/-/left-pad-1.3.0.tgz"}}}}`, "https://registry.npmjs.org")
	}))
	defer upstream.Close()

	repo := &domain.Repository{
		ID: "np-rw", Name: "npm-rw", Format: "npm",
		Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": upstream.URL},
	}
	r := setup(repo) // BaseURL: http://localhost:8080

	req := httptest.NewRequest(http.MethodGet, "/repository/npm-rw/left-pad", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "http://localhost:8080/repository/npm-rw/left-pad/-/left-pad-1.3.0.tgz")
	assert.NotContains(t, body, "registry.npmjs.org")
}
