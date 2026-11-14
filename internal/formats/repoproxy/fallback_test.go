package repoproxy_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/formats/repoproxy"
)

// The fallback path names the same artifact under a second upstream name —
// a Docker Hub official image under "library/", which the upstream answers
// with 401 for the short name (#757). The fallback is used only when it
// actually serves the request.

func TestServeGET_FallbackServesTheRequestWhenThePrimaryNameIsUnknown(t *testing.T) {
	useUnguardedUpstream(t)
	var mu sync.Mutex
	var paths []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		if r.URL.Path == "/v2/library/nginx/manifests/latest" {
			_, _ = fmt.Fprint(w, "official manifest")
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer upstream.Close()

	repo := proxyRepo("pfallback", upstream.URL)
	d := makeDeps(repo)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/v2/nginx/manifests/latest", nil)

	var used bool
	opts := repoproxy.ServeOptions{
		FallbackUpstreamPath: "/v2/library/nginx/manifests/latest",
		OnFallbackUsed:       func() { used = true },
	}
	require.NoError(t, repoproxy.ServeGETWithOptions(c, d, repo, "/v2/nginx/manifests/latest", "", base.Coords{}, "application/json", 0, opts))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "official manifest", w.Body.String())
	assert.True(t, used, "the caller is told the fallback served the request")

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{"/v2/nginx/manifests/latest", "/v2/library/nginx/manifests/latest"}, paths)
}

func TestServeGET_FallbackFailureLeavesThePrimaryAnswer(t *testing.T) {
	useUnguardedUpstream(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprint(w, "no such name")
	}))
	defer upstream.Close()

	repo := proxyRepo("pfallbackfail", upstream.URL)
	d := makeDeps(repo)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/v2/nginx/manifests/latest", nil)

	var used bool
	opts := repoproxy.ServeOptions{
		FallbackUpstreamPath: "/v2/library/nginx/manifests/latest",
		OnFallbackUsed:       func() { used = true },
	}
	require.NoError(t, repoproxy.ServeGETWithOptions(c, d, repo, "/v2/nginx/manifests/latest", "", base.Coords{}, "application/json", 0, opts))

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "no such name", "the primary answer keeps its body")
	assert.False(t, used, "a fallback that did not serve the request is not reported as used")
}

func TestServeGET_SuccessfulPrimaryIsNotFollowedByTheFallback(t *testing.T) {
	useUnguardedUpstream(t)
	var mu sync.Mutex
	var paths []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		_, _ = fmt.Fprint(w, "manifest")
	}))
	defer upstream.Close()

	repo := proxyRepo("pnoFallback", upstream.URL)
	d := makeDeps(repo)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/v2/nginx/manifests/latest", nil)

	opts := repoproxy.ServeOptions{FallbackUpstreamPath: "/v2/library/nginx/manifests/latest"}
	require.NoError(t, repoproxy.ServeGETWithOptions(c, d, repo, "/v2/nginx/manifests/latest", "", base.Coords{}, "application/json", 0, opts))

	assert.Equal(t, http.StatusOK, w.Code)
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{"/v2/nginx/manifests/latest"}, paths, "the fallback is only for a name the upstream does not know")
}

func TestUpstreamMissesName(t *testing.T) {
	assert.True(t, repoproxy.UpstreamMissesName(http.StatusNotFound))
	assert.True(t, repoproxy.UpstreamMissesName(http.StatusUnauthorized))
	assert.False(t, repoproxy.UpstreamMissesName(http.StatusOK))
	assert.False(t, repoproxy.UpstreamMissesName(http.StatusForbidden))
	assert.False(t, repoproxy.UpstreamMissesName(http.StatusInternalServerError))
}

func TestIsDockerHubRemote(t *testing.T) {
	for _, remote := range []string{
		"https://registry-1.docker.io/",
		"https://registry.docker.io",
		"https://docker.io",
		"https://www.docker.io/",
	} {
		assert.True(t, repoproxy.IsDockerHubRemote(remote), remote)
	}
	for _, remote := range []string{
		"https://ghcr.io/",
		"https://quay.io/",
		"https://registry.example.com/",
		"not a url",
	} {
		assert.False(t, repoproxy.IsDockerHubRemote(remote), remote)
	}
}
