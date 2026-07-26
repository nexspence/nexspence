package nuget_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats/nuget"
)

func TestRewriteRegistration_PackageContentAndIDs(t *testing.T) {
	// #98: proxied registration pages embed absolute api.nuget.org URLs, so
	// clients following registration metadata pull .nupkg files from
	// upstream, bypassing the proxy cache.
	in := []byte(`{
		"@id": "https://api.nuget.org/v3/registration5-gz-semver2/newtonsoft.json/index.json",
		"count": 1,
		"items": [{
			"@id": "https://api.nuget.org/v3/registration5-gz-semver2/newtonsoft.json/page/1.0.0/13.0.1.json",
			"items": [{
				"@id": "https://api.nuget.org/v3/registration5-gz-semver2/newtonsoft.json/13.0.1.json",
				"packageContent": "https://api.nuget.org/v3-flatcontainer/newtonsoft.json/13.0.1/newtonsoft.json.13.0.1.nupkg",
				"catalogEntry": {
					"id": "Newtonsoft.Json",
					"version": "13.0.1",
					"listed": true
				}
			}]
		}]
	}`)
	local := "http://localhost:8080/repository/nuget-proxy"
	out := string(nuget.RewriteRegistration(in, local))

	// packageContent is rebuilt from catalogEntry (lowercased id), not string-munged.
	assert.Contains(t, out,
		`"packageContent":"http://localhost:8080/repository/nuget-proxy/v3/flatcontainer/newtonsoft.json/13.0.1/newtonsoft.json.13.0.1.nupkg"`)
	// @id URLs with a registration* segment are re-rooted at the local registration base.
	assert.Contains(t, out,
		`"@id":"http://localhost:8080/repository/nuget-proxy/v3/registration/newtonsoft.json/index.json"`)
	assert.Contains(t, out,
		`"@id":"http://localhost:8080/repository/nuget-proxy/v3/registration/newtonsoft.json/13.0.1.json"`)
	assert.NotContains(t, out, "api.nuget.org")
	// catalogEntry survives.
	assert.Contains(t, out, `"listed":true`)
}

func TestRewriteRegistration_MalformedBodyUnchanged(t *testing.T) {
	in := []byte("<html>not json</html>")
	out := nuget.RewriteRegistration(in, "http://x")
	assert.Equal(t, in, out)
}

func TestNuGet_Proxy_Registration_Rewrites(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"items":[{"items":[{"packageContent":"https://api.nuget.org/v3-flatcontainer/my.lib/2.1.0/my.lib.2.1.0.nupkg","catalogEntry":{"id":"My.Lib","version":"2.1.0"}}]}]}`)
	}))
	defer upstream.Close()

	repo := &domain.Repository{
		ID: "ng-rw", Name: "nuget-rw", Format: "nuget",
		Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": upstream.URL},
	}
	r := setup(repo) // BaseURL: http://localhost:8080

	req := httptest.NewRequest(http.MethodGet, "/repository/nuget-rw/v3/registration/my.lib/index.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "http://localhost:8080/repository/nuget-rw/v3/flatcontainer/my.lib/2.1.0/my.lib.2.1.0.nupkg")
	assert.NotContains(t, body, "api.nuget.org")
}
