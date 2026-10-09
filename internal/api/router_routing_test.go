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
