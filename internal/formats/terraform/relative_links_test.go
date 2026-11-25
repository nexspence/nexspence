package terraform_test

// #821: every URL the client is expected to follow must be root-relative.
// The server mints links from http.base_url, which is the server's own
// configuration — behind a reverse proxy it is not the host the client
// reached, so the link points somewhere unreachable. A root-relative path
// carries no host and resolves against the host the client already used.
//
// The terraform client resolves all three of this format's URLs against the
// request it is answering, so a relative reference is enough — verified
// against terraform 1.15.5: `terraform init` installs a provider and a module
// from a registry whose discovery document, download_url and X-Terraform-Get
// are all root-relative.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/formats/terraform"
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
	h := terraform.New(d)
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

// The service discovery document names the protocol endpoints without a host:
// terraform-svchost resolves a relative service URL against the discovery
// document's own URL.
func TestTerraform_Discovery_ServiceURLsAreRootRelative(t *testing.T) {
	r := setupWithBaseURL(hostedRepo("tf-rel-discovery"), wrongBaseURL)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/tf-rel-discovery/.well-known/terraform.json", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var body map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	assert.Equal(t, "/repository/tf-rel-discovery/v1/providers/", body["providers.v1"])
	assert.Equal(t, "/repository/tf-rel-discovery/v1/modules/", body["modules.v1"])
	assertNoAbsoluteURL(t, w.Body.String())
}

// The provider download URL is root-relative: terraform resolves the
// download_url against the URL of the request that carried it.
func TestTerraform_ProviderDownload_URLIsRootRelative(t *testing.T) {
	r := setupWithBaseURL(hostedRepo("tf-rel-provider"), wrongBaseURL)

	blob := []byte("fake-provider-zip-content")
	req := httptest.NewRequest(http.MethodPut,
		"/repository/tf-rel-provider/v1/providers/acme/widget/1.0.0/upload/linux/amd64",
		bytes.NewReader(blob))
	req.Header.Set("Content-Type", "application/zip")
	req.ContentLength = int64(len(blob))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)

	req = httptest.NewRequest(http.MethodGet,
		"/repository/tf-rel-provider/v1/providers/acme/widget/1.0.0/download/linux/amd64", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var body map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	assert.Equal(t, "/repository/tf-rel-provider/v1/providers/acme/widget/1.0.0/linux_amd64.zip",
		body["download_url"])
	assertNoAbsoluteURL(t, w.Body.String())
}

// The module download URL is root-relative too: terraform resolves a
// "/" -prefixed X-Terraform-Get against the URL of the download request.
func TestTerraform_ModuleDownload_URLIsRootRelative(t *testing.T) {
	r := setupWithBaseURL(hostedRepo("tf-rel-module"), wrongBaseURL)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/tf-rel-module/v1/modules/acme/widget/aws/1.0.0/download", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())

	loc := w.Header().Get("X-Terraform-Get")
	assert.Equal(t, "/repository/tf-rel-module/v1/modules/acme/widget/aws/1.0.0.tar.gz", loc)
	assert.NotContains(t, loc, "http://", "the download URL must not carry a host")
	assert.NotContains(t, loc, wrongBaseURL, "the configured base URL must not reach the client")
}

// The proxied version document's download_url is rewritten onto a
// root-relative path under this repository.
func TestTerraform_Proxy_RewritesDownloadURLAsRootRelative(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"os":           "linux",
			"arch":         "amd64",
			"download_url": "https://releases.example.com/terraform-provider-widget/1.0.0/widget_linux_amd64.zip",
		})
	}))
	defer upstream.Close()

	repo := &domain.Repository{
		ID: "tf-rel-proxy", Name: "tf-rel-proxy", Format: "terraform",
		Type: domain.TypeProxy, Online: true,
		ProxyConfig: map[string]any{"remote_url": upstream.URL},
	}
	r := setupWithBaseURL(repo, wrongBaseURL)

	req := httptest.NewRequest(http.MethodGet,
		"/repository/tf-rel-proxy/v1/providers/acme/widget/versions", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var body map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	assert.Equal(t, "/repository/tf-rel-proxy/terraform-provider-widget/1.0.0/widget_linux_amd64.zip",
		body["download_url"])
	assertNoAbsoluteURL(t, w.Body.String())
}

// The served documents must not depend on the request's Host header. A link
// built from the request's host would be the cache-poisoning vector this fix
// exists to avoid — and it would be invisible: every other test sends the
// usual host, so such a change would still pass them all. The same request
// served under a foreign host must return the very same bytes.
func TestTerraform_Responses_DoNotDependOnRequestHost(t *testing.T) {
	for _, tc := range []struct {
		name   string
		target string
		code   int
	}{
		{"discovery", "/repository/tf-rel-hostdep/.well-known/terraform.json", http.StatusOK},
		{"provider versions", "/repository/tf-rel-hostdep/v1/providers/acme/widget/versions", http.StatusOK},
		{"module download", "/repository/tf-rel-hostdep/v1/modules/acme/widget/aws/1.0.0/download", http.StatusNoContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := setupWithBaseURL(hostedRepo("tf-rel-hostdep"), "http://localhost:8080")

			// The module download answers 204 with the URL in a header, so the
			// header is compared next to the body.
			serve := func(host string) ([]byte, string) {
				req := httptest.NewRequest(http.MethodGet, tc.target, nil)
				req.Host = host
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				require.Equal(t, tc.code, w.Code, w.Body.String())
				return w.Body.Bytes(), w.Header().Get("X-Terraform-Get")
			}

			normalBody, normalGet := serve("localhost")
			foreignBody, foreignGet := serve("myrepo.ru")

			assert.Equal(t, normalBody, foreignBody,
				"the served response must not depend on the request's Host header")
			assert.Equal(t, normalGet, foreignGet,
				"the served header must not depend on the request's Host header")
			assert.NotEmpty(t, string(normalBody)+normalGet,
				"the comparison must not be vacuous")
		})
	}
}
