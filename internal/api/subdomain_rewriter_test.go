package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/nexspence-oss/nexspence/internal/api"
)

func capturePathHandler() (http.Handler, *string) {
	captured := new(string)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*captured = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}), captured
}

// The subdomain is spliced into the URL path, so it has to look like a
// repository name. RBAC still runs on whatever name comes out, so a strange
// value is not a bypass — but a host header should not be able to put
// arbitrary characters, path separators or encoded segments into the path.
func TestSubdomainRewriter_RejectsNonRepoNameSubdomains(t *testing.T) {
	for _, host := range []string{
		"my%2frepo.nexspence.example.com",
		"my_repo.nexspence.example.com",
		"my repo.nexspence.example.com",
		"-leading.nexspence.example.com",
		"trailing-.nexspence.example.com",
	} {
		h, captured := capturePathHandler()
		rw := api.NewSubdomainRewriter(h, "nexspence.example.com")

		req := httptest.NewRequest(http.MethodGet, "/v2/alpine/manifests/latest", nil)
		req.Host = host
		rw.ServeHTTP(httptest.NewRecorder(), req)

		assert.Equal(t, "/v2/alpine/manifests/latest", *captured,
			"host %q must not be spliced into the path", host)
	}
}

func TestSubdomainRewriter_AcceptsRepoNameSubdomains(t *testing.T) {
	for _, host := range []string{
		"myrepo.nexspence.example.com",
		"my-repo-2.nexspence.example.com",
		"MyRepo.nexspence.example.com", // host matching is case-insensitive
	} {
		h, captured := capturePathHandler()
		rw := api.NewSubdomainRewriter(h, "nexspence.example.com")

		req := httptest.NewRequest(http.MethodGet, "/v2/alpine/manifests/latest", nil)
		req.Host = host
		rw.ServeHTTP(httptest.NewRecorder(), req)

		assert.NotEqual(t, "/v2/alpine/manifests/latest", *captured,
			"host %q is a valid repository name and should be rewritten", host)
	}
}

func TestSubdomainRewriter_NonDockerPath_Passthrough(t *testing.T) {
	h, captured := capturePathHandler()
	rw := api.NewSubdomainRewriter(h, "nexspence.example.com")

	req := httptest.NewRequest(http.MethodGet, "/repository/myrepo/some/file", nil)
	req.Host = "myrepo.nexspence.example.com"
	rw.ServeHTTP(httptest.NewRecorder(), req)

	assert.Equal(t, "/repository/myrepo/some/file", *captured)
}

func TestSubdomainRewriter_V2Root_Passthrough(t *testing.T) {
	h, captured := capturePathHandler()
	rw := api.NewSubdomainRewriter(h, "nexspence.example.com")

	req := httptest.NewRequest(http.MethodGet, "/v2/", nil)
	req.Host = "myrepo.nexspence.example.com"
	rw.ServeHTTP(httptest.NewRecorder(), req)

	assert.Equal(t, "/v2/", *captured)
}

// The token endpoint is registry-level like the ping: rewritten into a repo
// dispatch it would 404 and break docker login/pull on subdomain hosts.
func TestSubdomainRewriter_V2Token_Passthrough(t *testing.T) {
	h, captured := capturePathHandler()
	rw := api.NewSubdomainRewriter(h, "nexspence.example.com")

	req := httptest.NewRequest(http.MethodGet, "/v2/token", nil)
	req.Host = "myrepo.nexspence.example.com"
	rw.ServeHTTP(httptest.NewRecorder(), req)

	assert.Equal(t, "/v2/token", *captured)
}

func TestSubdomainRewriter_V2ManifestPath_RepoInjected(t *testing.T) {
	h, captured := capturePathHandler()
	rw := api.NewSubdomainRewriter(h, "nexspence.example.com")

	req := httptest.NewRequest(http.MethodGet, "/v2/alpine/manifests/latest", nil)
	req.Host = "myrepo.nexspence.example.com"
	rw.ServeHTTP(httptest.NewRecorder(), req)

	assert.Equal(t, "/v2/myrepo/alpine/manifests/latest", *captured)
}

func TestSubdomainRewriter_V2BlobPath_RepoInjected(t *testing.T) {
	h, captured := capturePathHandler()
	rw := api.NewSubdomainRewriter(h, "nexspence.example.com")

	req := httptest.NewRequest(http.MethodGet, "/v2/myimage/blobs/sha256:abc123", nil)
	req.Host = "releases.nexspence.example.com"
	rw.ServeHTTP(httptest.NewRecorder(), req)

	assert.Equal(t, "/v2/releases/myimage/blobs/sha256:abc123", *captured)
}

func TestSubdomainRewriter_HostWithPort_RepoInjected(t *testing.T) {
	h, captured := capturePathHandler()
	rw := api.NewSubdomainRewriter(h, "nexspence.example.com")

	req := httptest.NewRequest(http.MethodGet, "/v2/alpine/tags/list", nil)
	req.Host = "myrepo.nexspence.example.com:443"
	rw.ServeHTTP(httptest.NewRecorder(), req)

	assert.Equal(t, "/v2/myrepo/alpine/tags/list", *captured)
}

func TestSubdomainRewriter_NonMatchingHost_Passthrough(t *testing.T) {
	h, captured := capturePathHandler()
	rw := api.NewSubdomainRewriter(h, "nexspence.example.com")

	req := httptest.NewRequest(http.MethodGet, "/v2/alpine/manifests/latest", nil)
	req.Host = "other.example.com"
	rw.ServeHTTP(httptest.NewRecorder(), req)

	assert.Equal(t, "/v2/alpine/manifests/latest", *captured)
}

func TestSubdomainRewriter_BaseDomainDirectAccess_Passthrough(t *testing.T) {
	h, captured := capturePathHandler()
	rw := api.NewSubdomainRewriter(h, "nexspence.example.com")

	req := httptest.NewRequest(http.MethodGet, "/v2/myrepo/alpine/manifests/latest", nil)
	req.Host = "nexspence.example.com"
	rw.ServeHTTP(httptest.NewRecorder(), req)

	assert.Equal(t, "/v2/myrepo/alpine/manifests/latest", *captured)
}

func TestSubdomainRewriter_DeepSubdomain_Passthrough(t *testing.T) {
	h, captured := capturePathHandler()
	rw := api.NewSubdomainRewriter(h, "nexspence.example.com")

	req := httptest.NewRequest(http.MethodGet, "/v2/alpine/manifests/latest", nil)
	req.Host = "a.b.nexspence.example.com"
	rw.ServeHTTP(httptest.NewRecorder(), req)

	assert.Equal(t, "/v2/alpine/manifests/latest", *captured)
}
