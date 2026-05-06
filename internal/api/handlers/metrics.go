package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
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

// MetricsHandler serves GET /api/v1/metrics.
// Persistent artifact/byte counts are read from DB to stay accurate across nodes.
func MetricsHandler(pool *pgxpool.Pool) gin.HandlerFunc {
	return func(c *gin.Context) {
		snap := metrics.Snapshot()

		var artifactCount, bytesStored, downloadsTotal int64
		_ = pool.QueryRow(c.Request.Context(),
			`SELECT COUNT(*), COALESCE(SUM(size_bytes),0), COALESCE(SUM(download_count),0) FROM assets`,
		).Scan(&artifactCount, &bytesStored, &downloadsTotal)

		snap["artifacts_stored"] = artifactCount
		snap["bytes_stored"] = bytesStored
		snap["downloads_total"] = downloadsTotal

		c.JSON(http.StatusOK, snap)
	}
}
