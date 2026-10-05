package nuget_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
)

// #631: nuget.org's index advertises RepositorySignatures. Rewritten onto an
// http proxy URL, dotnet refuses the whole source (NU1301: must be served over
// HTTPS), so the proxy leaves the resource out; the rest is still re-rooted.
func TestNuGet_ProxyServiceIndex_OmitsRepositorySignatures(t *testing.T) {
	upstream := realShapeNuGetUpstream(t)
	r := setup(&domain.Repository{
		ID: "rp631", Name: "nuget-sig", Format: "nuget",
		Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": upstream.URL},
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/repository/nuget-sig/index.json", nil))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	body := w.Body.String()
	assert.NotContains(t, body, "RepositorySignatures")
	assert.NotContains(t, body, "repository-signatures")
	assert.Contains(t, body, "/repository/nuget-sig/v3-flatcontainer/")
	assert.Contains(t, body, "/repository/nuget-sig/v3/registration5-gz-semver2/")
}
