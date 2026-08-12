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

func scrape(t *testing.T, public bool) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	registerMetricsRoute(r, public, denyAll())

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	return w
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
