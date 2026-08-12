package api

import (
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/nexspence-oss/nexspence/internal/metrics"
)

// registerMetricsRoute mounts the Prometheus scrape endpoint.
//
// The endpoint requires a Bearer token (JWT or nxs_*) unless metrics.public is
// set: it shares the public listener with the API, so an anonymous scrape
// publishes install size, artifact and download counts and the Go runtime
// fingerprint. Deployments that keep the listener on a trusted network scrape
// it without a token secret instead.
func registerMetricsRoute(r *gin.Engine, public bool, authMW gin.HandlerFunc) {
	handler := gin.WrapH(promhttp.HandlerFor(metrics.Registry, promhttp.HandlerOpts{}))
	if public {
		r.GET("/metrics", handler)
		return
	}
	r.GET("/metrics", authMW, handler)
}
