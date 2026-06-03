package handlers

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nexspence-oss/nexspence/internal/redisclient"
)

// LivenessHandler returns 200 {"status":"ok"} — process is alive.
func LivenessHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	}
}

// ReadinessHandler checks DB and Redis connectivity in parallel.
// Either dep may be nil — it is skipped in that case.
// Returns 503 if any check fails.
func ReadinessHandler(pool *pgxpool.Pool, redis *redisclient.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		checks := map[string]string{}
		var mu sync.Mutex
		var wg sync.WaitGroup
		failed := false

		if pool != nil {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
				defer cancel()
				status := "ok"
				if err := pool.Ping(ctx); err != nil {
					status = "error"
				}
				mu.Lock()
				checks["db"] = status
				if status != "ok" {
					failed = true
				}
				mu.Unlock()
			}()
		}

		if redis != nil {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
				defer cancel()
				status := "ok"
				if err := redis.Ping(ctx); err != nil {
					status = "error"
				}
				mu.Lock()
				checks["redis"] = status
				if status != "ok" {
					failed = true
				}
				mu.Unlock()
			}()
		}

		wg.Wait()

		statusStr := "ok"
		code := http.StatusOK
		if failed {
			statusStr = "degraded"
			code = http.StatusServiceUnavailable
		}

		resp := gin.H{"status": statusStr}
		if len(checks) > 0 {
			resp["checks"] = checks
		}
		c.JSON(code, resp)
	}
}
