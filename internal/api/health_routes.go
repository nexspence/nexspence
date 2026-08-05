package api

import "github.com/gin-gonic/gin"

// registerHealthRoutes mounts the liveness and readiness probes.
//
// It exists to make the ordering explicit: gin captures the middleware chain
// when a route is registered, so these have to be added after r.Use(...) or
// they run bare — no Recovery, no metrics, no limits. They are still
// unauthenticated, which is the point of a probe.
func registerHealthRoutes(r *gin.Engine, liveness, readiness gin.HandlerFunc) {
	r.GET("/healthz", liveness)
	r.GET("/readyz", readiness)
}
