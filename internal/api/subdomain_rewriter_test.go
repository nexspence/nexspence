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
