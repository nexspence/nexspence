package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// countingHandler stands in for a format handler: any call means the request
// got past routing and could have reached the cache or the upstream.
type countingHandler struct{ calls int }

func (h *countingHandler) Name() string { return "raw" }
func (h *countingHandler) ServeHTTP(c *gin.Context) {
	h.calls++
	c.Status(http.StatusOK)
}

func proxyWithBlockRule(t *testing.T, format domain.RepoFormat) (*domain.Repository, *testutil.RoutingRuleRepo) {
	t.Helper()
	id := "rr"
	rules := testutil.NewRoutingRuleRepo()
	require.NoError(t, rules.Create(context.Background(),
		&domain.RoutingRule{ID: id, Name: id, Mode: "BLOCK", Matchers: []string{`acme`}}))
	return &domain.Repository{ID: "p", Name: "p", Format: format, Type: domain.TypeProxy,
		Online: true, AllowAnonymous: true, RoutingRuleID: &id}, rules
}

// A proxy's routing rule is applied before its format handler runs (#642).
func TestServeRepository_ProxyRoutingRule(t *testing.T) {
	gin.SetMode(gin.TestMode)
	proxy, rules := proxyWithBlockRule(t, "raw")
	h := &countingHandler{}
	r := gin.New()
	r.Any("/repository/:repoName/*path",
		serveRepository(testutil.NewRepoRepo(proxy), rules, h, map[string]formats.FormatHandler{"raw": h}))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/repository/p/com/acme/x.jar", nil))
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Zero(t, h.calls, "a refused path must not reach the format handler")

	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/repository/p/org/x.jar", nil))
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 1, h.calls)
}

// Same on the docker /v2/ routes, matched on the /v2/... path a group's rule
// sees: no manifest or token request may reach the upstream.
func TestServeDockerV2_ProxyRoutingRule(t *testing.T) {
	gin.SetMode(gin.TestMode)
	proxy, rules := proxyWithBlockRule(t, domain.FormatDocker)
	h := &countingHandler{}
	r := gin.New()
	r.Any("/v2/:repoName/*dockerpath",
		serveDockerV2(testutil.NewRepoRepo(proxy), rules, h, map[string]formats.FormatHandler{"docker": h}))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v2/p/acme/app/manifests/1.0", nil))
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Zero(t, h.calls)

	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v2/p/library/alpine/manifests/3", nil))
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 1, h.calls)
}

// proxyBlockingPrivate is a proxy whose rule blocks the anchored /private/
// prefix, the shape of rule a spelling trick has to get past.
func proxyBlockingPrivate(t *testing.T, format domain.RepoFormat, matcher string) (*domain.Repository, *testutil.RoutingRuleRepo) {
	t.Helper()
	id := "rr-private"
	rules := testutil.NewRoutingRuleRepo()
	require.NoError(t, rules.Create(context.Background(),
		&domain.RoutingRule{ID: id, Name: id, Mode: "BLOCK", Matchers: []string{matcher}}))
	return &domain.Repository{ID: "p", Name: "p", Format: format, Type: domain.TypeProxy,
		Online: true, AllowAnonymous: true, RoutingRuleID: &id}, rules
}

// The format handler serves path.Clean of the request path, so the rule must
// judge that same string: a second spelling of a blocked path must not reach
// the handler (and with it the cache or the upstream).
func TestServeRepository_ProxyRoutingRule_NonCanonicalSpellingRefused(t *testing.T) {
	gin.SetMode(gin.TestMode)
	proxy, rules := proxyBlockingPrivate(t, "raw", `^/private/`)
	h := &countingHandler{}
	r := gin.New()
	r.Any("/repository/:repoName/*path",
		serveRepository(testutil.NewRepoRepo(proxy), rules, h, map[string]formats.FormatHandler{"raw": h}))

	for _, target := range []string{
		"/repository/p//private/x",
		"/repository/p/./private/x",
		"/repository/p/%2e/private/x",
		"/repository/p/%2Fprivate/x",
		"/repository/p/%252e/private/x",
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
		assert.NotEqual(t, http.StatusOK, w.Code, target)
	}
	assert.Zero(t, h.calls, "no spelling of a blocked path may reach the format handler")

	// A trailing slash is an ordinary spelling (PyPI's simple index needs it)
	// and is judged as the handler sees it.
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/repository/p/public/x/", nil))
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 1, h.calls)
}

// Same on the OCI routes, where serveDockerV2 rewrites the params to /v2/...
func TestServeDockerV2_ProxyRoutingRule_NonCanonicalSpellingRefused(t *testing.T) {
	gin.SetMode(gin.TestMode)
	proxy, rules := proxyBlockingPrivate(t, domain.FormatDocker, `^/v2/private/`)
	h := &countingHandler{}
	r := gin.New()
	r.Any("/v2/:repoName/*dockerpath",
		serveDockerV2(testutil.NewRepoRepo(proxy), rules, h, map[string]formats.FormatHandler{"docker": h}))

	for _, target := range []string{
		"/v2/p//private/app/manifests/1",
		"/v2/p/./private/app/manifests/1",
		"/v2/p/%2e/private/app/manifests/1",
		"/v2/p/%2Fprivate/app/manifests/1",
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
		assert.NotEqual(t, http.StatusOK, w.Code, target)
	}
	assert.Zero(t, h.calls)
}
