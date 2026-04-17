package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/metrics"
)

// MetricsMiddleware increments request counters after each request.
func MetricsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		metrics.RequestsTotal.Add(1)
		c.Next()
		if c.Writer.Status() >= 500 {
			metrics.RequestErrors.Add(1)
		}
	}
}

// MetricsHandler serves GET /api/v1/metrics — simple JSON, no external deps.
func MetricsHandler(c *gin.Context) {
	c.JSON(http.StatusOK, metrics.Snapshot())
}
