package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

// The health probes were registered before r.Use(...). Gin snapshots the
// middleware chain at registration time, so they ran with no Recovery — a panic
// in the readiness path took the process down instead of returning 500 — and no
// metrics, body limit or rate limit either.
func TestHealthRoutes_RunInsideTheMiddlewareChain(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	saw := false
	r.Use(func(c *gin.Context) { saw = true; c.Next() })
	registerHealthRoutes(r, func(c *gin.Context) { c.Status(http.StatusOK) },
		func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, saw, "middleware registered before the route must run for it")
}

func TestHealthRoutes_PanicIsRecovered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	registerHealthRoutes(r,
		func(c *gin.Context) { c.Status(http.StatusOK) },
		func(*gin.Context) { panic("db driver blew up") })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	assert.Equal(t, http.StatusInternalServerError, w.Code,
		"a panic in the probe must not escape the handler")
}
