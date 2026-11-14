package oci_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
)

// manifestRequestFor asks the registry for one path, the way a client would.
func manifestRequestFor(r *gin.Engine, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Accept", "application/vnd.oci.image.manifest.v1+json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// Docker Hub keeps its official images under "library/" and answers a short
// name with 401 and a Bearer challenge — it does not know the name at all. The
// proxy must ask for "library/nginx" after the short name is refused, and
// serve the client the image it asked for (#757).

const libraryManifest = `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{},"layers":[]}`

// fakeHub answers like Docker Hub: the official image is served under
// "library/", and any other name is refused with a Bearer challenge.
type fakeHub struct {
	*httptest.Server
	mu    sync.Mutex
	paths []string
}

func newFakeHub(t *testing.T) *fakeHub {
	t.Helper()
	hub := &fakeHub{}
	hub.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.record(r.URL.Path)
		switch {
		case r.URL.Path == "/token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"token":"fake-token"}`))
		case strings.HasPrefix(r.URL.Path, "/v2/library/"):
			w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
			_, _ = w.Write([]byte(libraryManifest))
		default:
			// The name is not served. Hub answers with a challenge and, once
			// the token for that scope is presented, refuses the scope.
			w.Header().Set("WWW-Authenticate", `Bearer realm="`+hub.URL+`/token",service="registry.docker.io",scope="`+hubScope(r.URL.Path)+`"`)
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	t.Cleanup(hub.Close)
	return hub
}

func (h *fakeHub) record(path string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.paths = append(h.paths, path)
}

// registryPaths returns the registry paths asked for, without the token
// endpoint and without the repeats the Bearer challenge produces.
func (h *fakeHub) registryPaths() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for _, p := range h.paths {
		if p == "/token" {
			continue
		}
		if len(out) > 0 && out[len(out)-1] == p {
			continue
		}
		out = append(out, p)
	}
	return out
}

// hubScope builds the pull scope of a registry path.
func hubScope(path string) string {
	parts := strings.Split(strings.TrimPrefix(path, "/v2/"), "/")
	for i, p := range parts {
		if p == "manifests" || p == "blobs" || p == "referrers" {
			return "repository:" + strings.Join(parts[:i], "/") + ":pull"
		}
	}
	return ""
}

// hubTransport sends every request to the fake registry, whatever host it
// names — the proxy believes it is talking to Docker Hub.
type hubTransport struct{ target *url.URL }

func (t hubTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	clone := r.Clone(r.Context())
	clone.URL.Scheme = t.target.Scheme
	clone.URL.Host = t.target.Host
	return http.DefaultTransport.RoundTrip(clone)
}

// useFakeUpstream points the shared upstream client at the fake registry for
// the duration of the test.
func useFakeUpstream(t *testing.T, target string) {
	t.Helper()
	parsed, err := url.Parse(target)
	require.NoError(t, err)
	prev := repoproxy.UpstreamClient
	repoproxy.UpstreamClient = &http.Client{Transport: hubTransport{target: parsed}}
	t.Cleanup(func() { repoproxy.UpstreamClient = prev })
}

func hubProxyRepo() *domain.Repository {
	return &domain.Repository{
		ID: "proxy", Name: "proxy", Format: domain.FormatDocker, Type: domain.TypeProxy,
		ProxyConfig: map[string]any{"remote_url": "https://registry-1.docker.io/"},
	}
}

func TestProxyManifest_ShortNameFallsBackToLibrary(t *testing.T) {
	hub := newFakeHub(t)
	useFakeUpstream(t, hub.URL)
	_, d := setupWithDeps(hubProxyRepo())
	r := manifestRouter(d)

	w := manifestRequestFor(r, "/repository/proxy/v2/nginx/manifests/latest")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, libraryManifest, w.Body.String())

	paths := hub.registryPaths()
	require.Equal(t, "/v2/nginx/manifests/latest", paths[0], "the short name is tried first")
	require.Equal(t, "/v2/library/nginx/manifests/latest", paths[len(paths)-1], "the library name is tried after it")
}

func TestProxyManifest_ShortNameIsCachedUnderTheNameTheClientAskedFor(t *testing.T) {
	hub := newFakeHub(t)
	useFakeUpstream(t, hub.URL)
	_, d := setupWithDeps(hubProxyRepo())
	r := manifestRouter(d)

	w := manifestRequestFor(r, "/repository/proxy/v2/alpine/manifests/latest")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	asset, err := d.Assets.GetByPath(t.Context(), "proxy", "/manifests/alpine/latest")
	require.NoError(t, err)
	require.NotNil(t, asset, "the manifest is cached under the name the client asked for")
}

func TestProxyManifest_ShortNameGoesStraightToLibraryOnceItIsKnown(t *testing.T) {
	hub := newFakeHub(t)
	useFakeUpstream(t, hub.URL)
	_, d := setupWithDeps(hubProxyRepo())
	r := manifestRouter(d)

	require.Equal(t, http.StatusOK, manifestRequestFor(r, "/repository/proxy/v2/redis/manifests/latest").Code)
	before := len(hub.registryPaths())
	require.Equal(t, http.StatusOK, manifestRequestFor(r, "/repository/proxy/v2/redis/manifests/7.0").Code)

	// The second request does not repeat the refused short name.
	for _, p := range hub.registryPaths()[before:] {
		require.NotEqual(t, "/v2/redis/manifests/7.0", p)
	}
}

func TestProxyBlob_ShortNameFallsBackToLibrary(t *testing.T) {
	hub := newFakeHub(t)
	useFakeUpstream(t, hub.URL)
	_, d := setupWithDeps(hubProxyRepo())
	r := manifestRouter(d)

	digest := "sha256:" + strings.Repeat("a", 64)
	w := manifestRequestFor(r, "/repository/proxy/v2/busybox/blobs/"+digest)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	paths := hub.registryPaths()
	require.Equal(t, "/v2/busybox/blobs/"+digest, paths[0], "the short name is tried first")
	require.Equal(t, "/v2/library/busybox/blobs/"+digest, paths[len(paths)-1], "the library name is tried after it")
}

func TestProxyManifest_ShortNameIsNotRewrittenForANonHubUpstream(t *testing.T) {
	var paths []string
	var mu sync.Mutex
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		http.NotFound(w, r)
	}))
	defer up.Close()

	_, d := setupWithDeps(proxyManifestRepo(up.URL))
	r := manifestRouter(d)

	w := manifestRequestFor(r, "/repository/proxy/v2/nginx/manifests/latest")
	require.Equal(t, http.StatusNotFound, w.Code)

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []string{"/v2/nginx/manifests/latest"}, paths, "only Docker Hub has the library namespace")
}

func TestProxyManifest_NamespacedNameIsNotRewritten(t *testing.T) {
	hub := newFakeHub(t)
	useFakeUpstream(t, hub.URL)
	_, d := setupWithDeps(hubProxyRepo())
	r := manifestRouter(d)

	// "bitnami/nginx" is a namespaced name the upstream knows by that name —
	// but Hub does not host it either, so the answer stays what upstream said.
	w := manifestRequestFor(r, "/repository/proxy/v2/bitnami/nginx/manifests/latest")
	require.Equal(t, http.StatusUnauthorized, w.Code)

	for _, p := range hub.registryPaths() {
		require.NotContains(t, p, "library/bitnami")
	}
}
