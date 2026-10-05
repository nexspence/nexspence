package nuget_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
)

// #629: the service index URL is what NuGet calls the feed URL, and Nexus's
// default remote for a NuGet proxy. As remote_url it must resolve to the
// origin like a bare or /v3 one, for discovery and for every resource path.
func TestNuGet_ProxyServiceIndexRemoteURL_Works(t *testing.T) {
	upstream := realShapeNuGetUpstream(t)
	for _, suffix := range []string{"/v3/index.json", "/v3/index.json/", "/index.json"} {
		t.Run(suffix, func(t *testing.T) {
			repo := &domain.Repository{
				ID: "rp629", Name: "nuget-idx", Format: "nuget",
				Type: domain.TypeProxy, Online: true,
				ProxyConfig: map[string]any{"remote_url": upstream.URL + suffix},
			}
			r := setup(repo)

			idx := httptest.NewRecorder()
			r.ServeHTTP(idx, httptest.NewRequest(http.MethodGet, "/repository/nuget-idx/index.json", nil))
			require.Equal(t, http.StatusOK, idx.Code, idx.Body.String())
			assert.Contains(t, idx.Body.String(), "/repository/nuget-idx/v3-flatcontainer/")

			pkg := httptest.NewRecorder()
			r.ServeHTTP(pkg, httptest.NewRequest(http.MethodGet,
				"/repository/nuget-idx/v3-flatcontainer/newtonsoft.json/13.0.3/newtonsoft.json.13.0.3.nupkg", nil))
			require.Equal(t, http.StatusOK, pkg.Code, pkg.Body.String())
			assert.Equal(t, "nupkg-bytes", pkg.Body.String())
		})
	}
}
