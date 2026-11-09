package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// denyAll stands in for the real AuthMiddleware: an unauthenticated scrape is
// exactly the request it rejects, so a 401 proves the middleware ran.
func denyAll() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.AbortWithStatus(http.StatusUnauthorized)
	}
}

// passThrough stands in for the rate limiter when rate limiting is off.
func passThrough() gin.HandlerFunc {
	return func(c *gin.Context) { c.Next() }
}

func scrape(t *testing.T, public bool) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	registerMetricsRoute(r, public, denyAll(), passThrough())

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	return w
}

// The rate limiter keys on the user ID the authentication middleware puts in
// the context, so it has to run behind it: a scrape rejected by auth must
// never reach the limiter, and a scrape that passes it must be counted
// against the user rather than against the address Prometheus scrapes from.
func TestMetricsRoute_RateLimitRunsAfterAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	limited := func(c *gin.Context) {
		c.AbortWithStatus(http.StatusTooManyRequests)
	}

	r := gin.New()
	registerMetricsRoute(r, false, denyAll(), limited)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	assert.Equal(t, http.StatusUnauthorized, w.Code,
		"auth must reject the scrape before the limiter can answer it")
}

func TestMetricsRoute_RequiresAuthByDefault(t *testing.T) {
	w := scrape(t, false)
	assert.Equal(t, http.StatusUnauthorized, w.Code,
		"/metrics must stay behind Bearer auth when metrics.public is false")
}

// With metrics.public the endpoint is a plain anonymous scrape target, so a
// Prometheus ServiceMonitor needs no token secret.
func TestMetricsRoute_PublicSkipsAuth(t *testing.T) {
	w := scrape(t, true)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "nexspence_goroutines",
		"the public endpoint must still serve the Prometheus text exposition")
}
