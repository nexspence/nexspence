# Extended Monitoring Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Prometheus `/metrics` endpoint, Grafana auto-provisioned stack, and tabbed UI dashboard with time-series charts + per-repo stats to Nexspence.

**Architecture:** prometheus/client_golang with custom Registry; `/metrics` secured via existing `AuthMiddleware`; background sampler goroutine (10s) feeds both a fixed-size ring buffer and Prometheus gauges from DB; MonitoringPage.tsx refactored into 3 tabs using recharts; `deploy/monitoring/` supports both `docker compose --profile monitoring` and standalone deploy via `extra_hosts`.

**Tech Stack:** Go `github.com/prometheus/client_golang` v1.20, `prom/prometheus:v2.51.0`, `grafana/grafana:10.4.0`, `recharts` v2, existing Gin/pgx stack.

---

## File Map

**Create:**
- `internal/metrics/ring_buffer.go` — DataPoint, RingBuffer, StartSampler, takeSample
- `internal/metrics/ring_buffer_test.go` — unit tests
- `internal/api/handlers/metrics_test.go` — handler tests
- `deploy/monitoring/prometheus.yml`
- `deploy/monitoring/prometheus-token.example`
- `deploy/monitoring/docker-compose.yml`
- `deploy/monitoring/grafana/provisioning/datasources/nexspence.yml`
- `deploy/monitoring/grafana/provisioning/dashboards/nexspence.yml`
- `deploy/monitoring/grafana/dashboards/nexspence.json`

**Modify:**
- `internal/metrics/metrics.go` — add Prometheus Registry + metric vars + `RecordRequest()` + `UpdateGauges()`
- `internal/api/handlers/metrics.go` — update `MetricsMiddleware`, add `HistoryHandler`, `ReposHandler`
- `internal/api/router.go` — wire `/metrics`, `/api/v1/metrics/history`, `/api/v1/metrics/repos`
- `cmd/server/main.go` — seed gauges on startup, call `StartSampler`
- `go.mod` / `go.sum`
- `docker-compose.yml` — add `prometheus` + `grafana` services + `grafana_data` volume
- `.gitignore` — add `deploy/monitoring/prometheus-token`
- `frontend/package.json` — add `recharts`
- `frontend/src/pages/MonitoringPage.tsx` — full rewrite

---

## Task 1: Add prometheus/client_golang dependency

**Files:** `go.mod`, `go.sum`

- [ ] Run from repo root:
```bash
go get github.com/prometheus/client_golang@v1.20.0
```
Expected output: go.mod updated with `github.com/prometheus/client_golang v1.20.0`.

- [ ] Verify no build errors:
```bash
go build ./...
```
Expected: exits 0.

- [ ] Commit:
```bash
git add go.mod go.sum
git commit -m "chore: add prometheus/client_golang v1.20.0 dependency"
```

---

## Task 2: Extend metrics.go with Prometheus Registry and helper functions

**Files:**
- Modify: `internal/metrics/metrics.go`

- [ ] Write a minimal placeholder test to verify the package compiles with new symbols (create file now, tests expanded in Task 3):
```
internal/metrics/ring_buffer_test.go
```
```go
package metrics_test

import "testing"

func TestPlaceholder(t *testing.T) {}
```

- [ ] Run to confirm it compiles:
```bash
go test ./internal/metrics/...
```
Expected: PASS.

- [ ] Replace `internal/metrics/metrics.go` entirely:
```go
package metrics

import (
	"runtime"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

var (
	RequestsTotal    atomic.Int64
	RequestErrors    atomic.Int64
	AuditEventsCount atomic.Int64
	ArtifactsDeleted atomic.Int64

	startTime = time.Now()
)

// Registry is the custom Prometheus registry for all Nexspence metrics.
// A custom registry prevents polluting the default registry and avoids
// duplicate-registration panics when tests import this package multiple times.
var Registry = prometheus.NewRegistry()

var (
	PromRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "nexspence_requests_total",
		Help: "Total HTTP requests processed.",
	}, []string{"method", "status_class"})

	PromRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "nexspence_request_duration_seconds",
		Help:    "HTTP request duration in seconds.",
		Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
	}, []string{"method"})

	PromArtifacts = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "nexspence_artifacts_total",
		Help: "Total number of artifacts currently stored.",
	})

	PromBytesStored = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "nexspence_bytes_stored_bytes",
		Help: "Total bytes stored across all repositories.",
	})

	PromDownloads = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "nexspence_downloads_total",
		Help: "Cumulative artifact download count.",
	})

	PromGoroutines = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "nexspence_goroutines",
		Help: "Current number of goroutines.",
	})

	PromMemoryAlloc = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "nexspence_memory_alloc_bytes",
		Help: "Current heap allocation in bytes.",
	})
)

func init() {
	Registry.MustRegister(
		PromRequestsTotal,
		PromRequestDuration,
		PromArtifacts,
		PromBytesStored,
		PromDownloads,
		PromGoroutines,
		PromMemoryAlloc,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
}

// RecordRequest records one HTTP request into Prometheus counters.
// Called by MetricsMiddleware after c.Next() returns.
func RecordRequest(method, statusClass string, d time.Duration) {
	PromRequestsTotal.WithLabelValues(method, statusClass).Inc()
	PromRequestDuration.WithLabelValues(method).Observe(d.Seconds())
}

// UpdateGauges refreshes DB-backed and runtime Prometheus gauges.
// Called by the background sampler every 10 s.
func UpdateGauges(artifacts, bytes, downloads int64) {
	PromArtifacts.Set(float64(artifacts))
	PromBytesStored.Set(float64(bytes))
	PromDownloads.Set(float64(downloads))

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	PromMemoryAlloc.Set(float64(mem.Alloc))
	PromGoroutines.Set(float64(runtime.NumGoroutine()))
}

// Snapshot returns a point-in-time snapshot of session and runtime metrics.
func Snapshot() Map {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	return Map{
		"uptime_seconds":     time.Since(startTime).Seconds(),
		"requests_total":     RequestsTotal.Load(),
		"request_errors":     RequestErrors.Load(),
		"audit_events_count": AuditEventsCount.Load(),
		"artifacts_deleted":  ArtifactsDeleted.Load(),
		"goroutines":         runtime.NumGoroutine(),
		"memory": Map{
			"alloc_bytes":       mem.Alloc,
			"total_alloc_bytes": mem.TotalAlloc,
			"sys_bytes":         mem.Sys,
			"gc_cycles":         mem.NumGC,
		},
	}
}

// Map is an alias for the map type used in JSON responses.
type Map = map[string]any
```

- [ ] Verify package compiles:
```bash
go build ./internal/metrics/...
```
Expected: exits 0.

- [ ] Commit:
```bash
git add internal/metrics/metrics.go internal/metrics/ring_buffer_test.go
git commit -m "feat(metrics): add Prometheus Registry and metric vars"
```

---

## Task 3: Add ring buffer and background sampler

**Files:**
- Create: `internal/metrics/ring_buffer.go`
- Modify: `internal/metrics/ring_buffer_test.go`

- [ ] Replace `internal/metrics/ring_buffer_test.go` with full tests:
```go
package metrics_test

import (
	"testing"

	"github.com/nexspence-oss/nexspence/internal/metrics"
	"github.com/stretchr/testify/assert"
)

func TestRingBuffer_EmptySnapshot(t *testing.T) {
	rb := &metrics.RingBuffer{}
	assert.Empty(t, rb.Snapshot())
}

func TestRingBuffer_AddAndSnapshot(t *testing.T) {
	rb := &metrics.RingBuffer{}
	rb.Add(metrics.DataPoint{Timestamp: 1, RequestsTotal: 10})
	rb.Add(metrics.DataPoint{Timestamp: 2, RequestsTotal: 20})

	snap := rb.Snapshot()
	assert.Len(t, snap, 2)
	assert.Equal(t, int64(1), snap[0].Timestamp)
	assert.Equal(t, int64(2), snap[1].Timestamp)
}

func TestRingBuffer_WrapsAround(t *testing.T) {
	rb := &metrics.RingBuffer{}
	for i := 0; i < 400; i++ {
		rb.Add(metrics.DataPoint{Timestamp: int64(i)})
	}
	snap := rb.Snapshot()
	assert.Len(t, snap, 360)
	assert.Equal(t, int64(40), snap[0].Timestamp)   // oldest = 400-360=40
	assert.Equal(t, int64(399), snap[359].Timestamp) // newest = 399
}

func TestRingBuffer_OrderPreserved(t *testing.T) {
	rb := &metrics.RingBuffer{}
	for i := 0; i < 5; i++ {
		rb.Add(metrics.DataPoint{Timestamp: int64(i * 10)})
	}
	snap := rb.Snapshot()
	for i := 1; i < len(snap); i++ {
		assert.Greater(t, snap[i].Timestamp, snap[i-1].Timestamp)
	}
}
```

- [ ] Run to see them fail:
```bash
go test ./internal/metrics/ -run TestRingBuffer -v
```
Expected: FAIL — `metrics.RingBuffer` and `metrics.DataPoint` not defined.

- [ ] Create `internal/metrics/ring_buffer.go`:
```go
package metrics

import (
	"context"
	"runtime"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const ringSize = 360 // 1 hour at 10-second intervals

// DataPoint is a single timestamped metrics snapshot in the ring buffer.
type DataPoint struct {
	Timestamp       int64 `json:"timestamp"`
	RequestsTotal   int64 `json:"requests_total"`
	RequestErrors   int64 `json:"request_errors"`
	ArtifactsStored int64 `json:"artifacts_stored"`
	BytesStored     int64 `json:"bytes_stored"`
	DownloadsTotal  int64 `json:"downloads_total"`
	Goroutines      int   `json:"goroutines"`
}

// RingBuffer is a fixed-size circular buffer of DataPoints.
type RingBuffer struct {
	mu   sync.RWMutex
	data [ringSize]DataPoint
	head int
	size int
}

// Add appends a DataPoint, overwriting the oldest entry when the buffer is full.
func (r *RingBuffer) Add(p DataPoint) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[r.head] = p
	r.head = (r.head + 1) % ringSize
	if r.size < ringSize {
		r.size++
	}
}

// Snapshot returns all stored DataPoints in chronological order (oldest first).
func (r *RingBuffer) Snapshot() []DataPoint {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.size == 0 {
		return nil
	}
	result := make([]DataPoint, r.size)
	start := (r.head - r.size + ringSize) % ringSize
	for i := 0; i < r.size; i++ {
		result[i] = r.data[(start+i)%ringSize]
	}
	return result
}

// History is the global ring buffer populated by StartSampler.
var History = &RingBuffer{}

// StartSampler starts a background goroutine that samples metrics every 10s
// and stops when ctx is cancelled.
func StartSampler(ctx context.Context, pool *pgxpool.Pool) {
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				takeSample(ctx, pool)
			case <-ctx.Done():
				return
			}
		}
	}()
}

func takeSample(ctx context.Context, pool *pgxpool.Pool) {
	var artifacts, bytes, downloads int64
	_ = pool.QueryRow(ctx,
		`SELECT COUNT(*), COALESCE(SUM(size_bytes),0), COALESCE(SUM(download_count),0) FROM assets`,
	).Scan(&artifacts, &bytes, &downloads)

	UpdateGauges(artifacts, bytes, downloads)

	History.Add(DataPoint{
		Timestamp:       time.Now().Unix(),
		RequestsTotal:   RequestsTotal.Load(),
		RequestErrors:   RequestErrors.Load(),
		ArtifactsStored: artifacts,
		BytesStored:     bytes,
		DownloadsTotal:  downloads,
		Goroutines:      runtime.NumGoroutine(),
	})
}
```

- [ ] Run ring buffer tests:
```bash
go test ./internal/metrics/ -run TestRingBuffer -v
```
Expected: 4 tests PASS.

- [ ] Run full suite:
```bash
go test ./... 2>&1 | tail -5
```
Expected: all pass.

- [ ] Commit:
```bash
git add internal/metrics/ring_buffer.go internal/metrics/ring_buffer_test.go
git commit -m "feat(metrics): add ring buffer with 1h history and background sampler"
```

---

## Task 4: Update MetricsMiddleware and add HistoryHandler + ReposHandler

**Files:**
- Modify: `internal/api/handlers/metrics.go`
- Create: `internal/api/handlers/metrics_test.go`

- [ ] Write failing tests `internal/api/handlers/metrics_test.go`:
```go
package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/api/handlers"
	"github.com/nexspence-oss/nexspence/internal/metrics"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func newTokenSvc() *service.TokenService {
	return service.NewTokenService(testutil.NewUserTokenRepo(), testutil.NewUserRepo())
}

func TestPrometheusHandler_NoToken_Returns401(t *testing.T) {
	r := gin.New()
	uSvc := service.NewUserService(testutil.NewUserRepo(), testutil.NewRoleRepo(), newAuthSvc(), zap.NewNop().Sugar())
	authMW := handlers.AuthMiddleware(uSvc, newTokenSvc())
	r.GET("/metrics", authMW, gin.WrapH(promhttp.HandlerFor(metrics.Registry, promhttp.HandlerOpts{})))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestPrometheusHandler_ValidToken_Returns200(t *testing.T) {
	user := activeUser("admin", "pass")
	r := gin.New()
	uSvc := service.NewUserService(testutil.NewUserRepo(user), testutil.NewRoleRepo(), newAuthSvc(), zap.NewNop().Sugar())
	authMW := handlers.AuthMiddleware(uSvc, newTokenSvc())
	r.GET("/metrics", authMW, gin.WrapH(promhttp.HandlerFor(metrics.Registry, promhttp.HandlerOpts{})))

	tok := bearerToken(newUserSvc(user), "admin")
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "text/plain")
}

func TestHistoryHandler_ReturnsEmptyArrayWhenNoData(t *testing.T) {
	r := gin.New()
	r.GET("/api/v1/metrics/history", handlers.HistoryHandler())

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/metrics/history", nil))
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "[]", w.Body.String())
}

func TestRecordRequest_DoesNotPanic(t *testing.T) {
	assert.NotPanics(t, func() {
		metrics.RecordRequest("POST", "2xx", 10*time.Millisecond)
	})
	mfs, err := metrics.Registry.Gather()
	assert.NoError(t, err)
	found := false
	for _, mf := range mfs {
		if mf.GetName() == "nexspence_requests_total" {
			found = true
		}
	}
	assert.True(t, found)
}

func newAuthSvc() interface{ HashPassword(string) (string, error) } {
	// reuse the auth.NewService helper already imported by auth_test.go helpers
	return newUserSvc().AuthService()
}
```

Wait — `newUserSvc().AuthService()` doesn't exist. Use the pattern from `auth_test.go` which defines `newUserSvc()`. Since we're in the same `handlers_test` package, those helpers are available. Remove `newAuthSvc()` and use `newAuthSvcDirect()`:

Replace `internal/api/handlers/metrics_test.go` with:
```go
package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/api/handlers"
	"github.com/nexspence-oss/nexspence/internal/auth"
	"github.com/nexspence-oss/nexspence/internal/metrics"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func TestPrometheusHandler_NoToken_Returns401(t *testing.T) {
	r := gin.New()
	authSvc := auth.NewService(testSecret, 24, bcryptCostTest)
	uSvc := service.NewUserService(testutil.NewUserRepo(), testutil.NewRoleRepo(), authSvc, zap.NewNop().Sugar())
	tSvc := service.NewTokenService(testutil.NewUserTokenRepo(), testutil.NewUserRepo())
	r.GET("/metrics", handlers.AuthMiddleware(uSvc, tSvc),
		gin.WrapH(promhttp.HandlerFor(metrics.Registry, promhttp.HandlerOpts{})))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestPrometheusHandler_ValidToken_Returns200(t *testing.T) {
	user := activeUser("admin", "pass")
	authSvc := auth.NewService(testSecret, 24, bcryptCostTest)
	uSvc := service.NewUserService(testutil.NewUserRepo(user), testutil.NewRoleRepo(), authSvc, zap.NewNop().Sugar())
	tSvc := service.NewTokenService(testutil.NewUserTokenRepo(), testutil.NewUserRepo(user))
	r := gin.New()
	r.GET("/metrics", handlers.AuthMiddleware(uSvc, tSvc),
		gin.WrapH(promhttp.HandlerFor(metrics.Registry, promhttp.HandlerOpts{})))

	tok := bearerToken(newUserSvc(user), "admin")
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "text/plain")
}

func TestHistoryHandler_EmptyBuffer_ReturnsEmptyArray(t *testing.T) {
	// Reset global buffer so previous test runs don't interfere
	metrics.History = &metrics.RingBuffer{}
	r := gin.New()
	r.GET("/api/v1/metrics/history", handlers.HistoryHandler())

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/metrics/history", nil))
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "[]", w.Body.String())
}

func TestHistoryHandler_WithData_ReturnsPoints(t *testing.T) {
	metrics.History = &metrics.RingBuffer{}
	metrics.History.Add(metrics.DataPoint{Timestamp: 1000, RequestsTotal: 42})
	r := gin.New()
	r.GET("/api/v1/metrics/history", handlers.HistoryHandler())

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/metrics/history", nil))
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"timestamp":1000`)
}

func TestRecordRequest_DoesNotPanic(t *testing.T) {
	assert.NotPanics(t, func() {
		metrics.RecordRequest("DELETE", "4xx", 10*time.Millisecond)
	})
}
```

- [ ] Run to see tests fail (handlers not updated yet):
```bash
go test ./internal/api/handlers/ -run "TestPrometheusHandler|TestHistoryHandler|TestRecordRequest" -v 2>&1 | head -30
```
Expected: compilation error — `handlers.HistoryHandler` undefined.

- [ ] Replace `internal/api/handlers/metrics.go`:
```go
package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nexspence-oss/nexspence/internal/metrics"
)

// MetricsMiddleware increments request counters after each request.
func MetricsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		metrics.RequestsTotal.Add(1)
		c.Next()
		status := c.Writer.Status()
		if status >= 500 {
			metrics.RequestErrors.Add(1)
		}
		statusClass := strconv.Itoa(status/100) + "xx"
		metrics.RecordRequest(c.Request.Method, statusClass, time.Since(start))
	}
}

// MetricsHandler serves GET /api/v1/metrics — JSON snapshot (unchanged, public).
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

// HistoryHandler serves GET /api/v1/metrics/history — returns ring buffer as JSON.
func HistoryHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		points := metrics.History.Snapshot()
		if points == nil {
			points = []metrics.DataPoint{}
		}
		c.JSON(http.StatusOK, points)
	}
}

// RepoMetric is a per-repository metrics row returned by ReposHandler.
type RepoMetric struct {
	Name      string `json:"name"`
	Format    string `json:"format"`
	Type      string `json:"type"`
	Downloads int64  `json:"downloads"`
	SizeBytes int64  `json:"size_bytes"`
}

// ReposHandler serves GET /api/v1/metrics/repos — top 10 repos by downloads.
func ReposHandler(pool *pgxpool.Pool) gin.HandlerFunc {
	return func(c *gin.Context) {
		rows, err := pool.Query(c.Request.Context(), `
			SELECT r.name, r.format, r.type,
			       COALESCE(SUM(a.download_count), 0),
			       COALESCE(SUM(a.size_bytes), 0)
			FROM repositories r
			LEFT JOIN assets a ON a.repository_id = r.id
			GROUP BY r.id, r.name, r.format, r.type
			ORDER BY COALESCE(SUM(a.download_count), 0) DESC
			LIMIT 10
		`)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		defer rows.Close()

		result := make([]RepoMetric, 0)
		for rows.Next() {
			var rm RepoMetric
			if err := rows.Scan(&rm.Name, &rm.Format, &rm.Type, &rm.Downloads, &rm.SizeBytes); err != nil {
				continue
			}
			result = append(result, rm)
		}
		c.JSON(http.StatusOK, result)
	}
}
```

- [ ] Run tests:
```bash
go test ./internal/api/handlers/ -run "TestPrometheusHandler|TestHistoryHandler|TestRecordRequest" -v
```
Expected: all PASS.

- [ ] Run full suite:
```bash
go test ./... 2>&1 | tail -5
```
Expected: all pass.

- [ ] Commit:
```bash
git add internal/api/handlers/metrics.go internal/api/handlers/metrics_test.go
git commit -m "feat(metrics): update MetricsMiddleware, add HistoryHandler and ReposHandler"
```

---

## Task 5: Wire routes in router.go and start sampler in main.go

**Files:**
- Modify: `internal/api/router.go`
- Modify: `cmd/server/main.go`

- [ ] In `internal/api/router.go`, add two imports to the existing import block:
```go
"github.com/nexspence-oss/nexspence/internal/metrics"
"github.com/prometheus/client_golang/prometheus/promhttp"
```

- [ ] In `internal/api/router.go`, after the line:
```go
r.GET("/api/v1/metrics", handlers.MetricsHandler(pool))
```
Add:
```go
// Prometheus scrape endpoint — requires Bearer auth (JWT or nxs_* token)
r.GET("/metrics", authMW, gin.WrapH(promhttp.HandlerFor(metrics.Registry, promhttp.HandlerOpts{})))
```

- [ ] In `internal/api/router.go`, inside the `authed` group block (after existing authed routes), add:
```go
authed.GET("/api/v1/metrics/history", handlers.HistoryHandler())
authed.GET("/api/v1/metrics/repos", handlers.ReposHandler(pool))
```

- [ ] Build to verify:
```bash
go build ./...
```
Expected: exits 0.

- [ ] In `cmd/server/main.go`, add to the import block:
```go
"github.com/nexspence-oss/nexspence/internal/metrics"
```

- [ ] In `cmd/server/main.go`, inside the `RunE` function, after `defer pool.Close()` and before `router := api.NewRouter(...)`, add:
```go
// Seed Prometheus gauges from DB on startup.
{
    var artifacts, bytes, downloads int64
    _ = pool.QueryRow(cmd.Context(),
        `SELECT COUNT(*), COALESCE(SUM(size_bytes),0), COALESCE(SUM(download_count),0) FROM assets`,
    ).Scan(&artifacts, &bytes, &downloads)
    metrics.UpdateGauges(artifacts, bytes, downloads)
    log.Info("metrics gauges seeded", "artifacts", artifacts, "bytes", bytes)
}

// Start background metrics sampler — stops on context cancellation.
samplerCtx, cancelSampler := context.WithCancel(cmd.Context())
defer cancelSampler()
metrics.StartSampler(samplerCtx, pool)
```

- [ ] Build to verify:
```bash
go build ./cmd/server/
```
Expected: exits 0.

- [ ] Run full suite:
```bash
go test ./... 2>&1 | tail -5
```
Expected: all pass.

- [ ] Commit:
```bash
git add internal/api/router.go cmd/server/main.go
git commit -m "feat(metrics): wire /metrics Prometheus endpoint and start background sampler"
```

---

## Task 6: Create deploy/monitoring/ — Prometheus config and Grafana provisioning

**Files:** all new under `deploy/monitoring/`

- [ ] Create `deploy/monitoring/prometheus.yml`:
```yaml
global:
  scrape_interval: 15s
  evaluation_interval: 15s

scrape_configs:
  - job_name: nexspence
    metrics_path: /metrics
    bearer_token_file: /etc/prometheus/token
    static_configs:
      - targets:
          - nexspence:8081
```

- [ ] Create `deploy/monitoring/prometheus-token.example`:
```
# Copy this file to deploy/monitoring/prometheus-token and replace with a real token.
# Create an API token via:   POST /api/v1/me/tokens   (with Bearer auth)
# Write it without trailing newline:
#   printf 'nxs_your_token_here' > deploy/monitoring/prometheus-token
nxs_replace_me
```

- [ ] Create `deploy/monitoring/grafana/provisioning/datasources/nexspence.yml`:
```yaml
apiVersion: 1
datasources:
  - name: Nexspence-Prometheus
    type: prometheus
    uid: nexspence-prom
    url: http://prometheus:9090
    access: proxy
    isDefault: true
    jsonData:
      timeInterval: 15s
```

- [ ] Create `deploy/monitoring/grafana/provisioning/dashboards/nexspence.yml`:
```yaml
apiVersion: 1
providers:
  - name: nexspence
    type: file
    options:
      path: /var/lib/grafana/dashboards
```

- [ ] Create `deploy/monitoring/grafana/dashboards/nexspence.json`:
```json
{
  "annotations": {"list": []},
  "editable": true,
  "fiscalYearStartMonth": 0,
  "graphTooltip": 0,
  "id": null,
  "links": [],
  "panels": [
    {
      "id": 1, "title": "Requests / sec", "type": "timeseries",
      "gridPos": {"h": 8, "w": 8, "x": 0, "y": 0},
      "targets": [{"datasource": {"type": "prometheus", "uid": "nexspence-prom"}, "expr": "sum(rate(nexspence_requests_total[1m]))", "legendFormat": "req/s", "refId": "A"}],
      "fieldConfig": {"defaults": {"color": {"mode": "palette-classic"}, "unit": "reqps"}, "overrides": []},
      "options": {"legend": {"displayMode": "list", "placement": "bottom"}, "tooltip": {"mode": "single"}}
    },
    {
      "id": 2, "title": "Error Rate %", "type": "timeseries",
      "gridPos": {"h": 8, "w": 8, "x": 8, "y": 0},
      "targets": [{"datasource": {"type": "prometheus", "uid": "nexspence-prom"}, "expr": "sum(rate(nexspence_requests_total{status_class=\"5xx\"}[1m])) / sum(rate(nexspence_requests_total[1m])) * 100", "legendFormat": "error %", "refId": "A"}],
      "fieldConfig": {"defaults": {"color": {"fixedColor": "red", "mode": "fixed"}, "unit": "percent", "min": 0, "max": 100}, "overrides": []},
      "options": {"legend": {"displayMode": "list", "placement": "bottom"}, "tooltip": {"mode": "single"}}
    },
    {
      "id": 3, "title": "Request Latency p95", "type": "timeseries",
      "gridPos": {"h": 8, "w": 8, "x": 16, "y": 0},
      "targets": [{"datasource": {"type": "prometheus", "uid": "nexspence-prom"}, "expr": "histogram_quantile(0.95, sum(rate(nexspence_request_duration_seconds_bucket[5m])) by (le))", "legendFormat": "p95", "refId": "A"}],
      "fieldConfig": {"defaults": {"color": {"fixedColor": "yellow", "mode": "fixed"}, "unit": "s"}, "overrides": []},
      "options": {"legend": {"displayMode": "list", "placement": "bottom"}, "tooltip": {"mode": "single"}}
    },
    {
      "id": 4, "title": "Storage Used", "type": "timeseries",
      "gridPos": {"h": 8, "w": 12, "x": 0, "y": 8},
      "targets": [{"datasource": {"type": "prometheus", "uid": "nexspence-prom"}, "expr": "nexspence_bytes_stored_bytes", "legendFormat": "bytes", "refId": "A"}],
      "fieldConfig": {"defaults": {"color": {"fixedColor": "green", "mode": "fixed"}, "unit": "bytes"}, "overrides": []},
      "options": {"legend": {"displayMode": "list", "placement": "bottom"}, "tooltip": {"mode": "single"}}
    },
    {
      "id": 5, "title": "Downloads / min", "type": "timeseries",
      "gridPos": {"h": 8, "w": 12, "x": 12, "y": 8},
      "targets": [{"datasource": {"type": "prometheus", "uid": "nexspence-prom"}, "expr": "rate(nexspence_downloads_total[1m]) * 60", "legendFormat": "dl/min", "refId": "A"}],
      "fieldConfig": {"defaults": {"color": {"fixedColor": "purple", "mode": "fixed"}, "unit": "short"}, "overrides": []},
      "options": {"legend": {"displayMode": "list", "placement": "bottom"}, "tooltip": {"mode": "single"}}
    },
    {
      "id": 6, "title": "Artifacts Total", "type": "stat",
      "gridPos": {"h": 4, "w": 8, "x": 0, "y": 16},
      "targets": [{"datasource": {"type": "prometheus", "uid": "nexspence-prom"}, "expr": "nexspence_artifacts_total", "refId": "A"}],
      "fieldConfig": {"defaults": {"color": {"mode": "thresholds"}, "thresholds": {"steps": [{"color": "green", "value": null}]}, "unit": "short"}, "overrides": []},
      "options": {"colorMode": "value", "graphMode": "none", "justifyMode": "auto", "orientation": "auto", "reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": false}, "textMode": "auto"}
    },
    {
      "id": 7, "title": "Goroutines", "type": "stat",
      "gridPos": {"h": 4, "w": 8, "x": 8, "y": 16},
      "targets": [{"datasource": {"type": "prometheus", "uid": "nexspence-prom"}, "expr": "nexspence_goroutines", "refId": "A"}],
      "fieldConfig": {"defaults": {"color": {"mode": "thresholds"}, "thresholds": {"steps": [{"color": "green", "value": null}, {"color": "yellow", "value": 100}, {"color": "red", "value": 500}]}, "unit": "short"}, "overrides": []},
      "options": {"colorMode": "value", "graphMode": "none", "justifyMode": "auto", "orientation": "auto", "reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": false}, "textMode": "auto"}
    },
    {
      "id": 8, "title": "Memory Alloc", "type": "stat",
      "gridPos": {"h": 4, "w": 8, "x": 16, "y": 16},
      "targets": [{"datasource": {"type": "prometheus", "uid": "nexspence-prom"}, "expr": "nexspence_memory_alloc_bytes", "refId": "A"}],
      "fieldConfig": {"defaults": {"color": {"mode": "thresholds"}, "thresholds": {"steps": [{"color": "green", "value": null}, {"color": "yellow", "value": 536870912}, {"color": "red", "value": 1073741824}]}, "unit": "bytes"}, "overrides": []},
      "options": {"colorMode": "value", "graphMode": "none", "justifyMode": "auto", "orientation": "auto", "reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": false}, "textMode": "auto"}
    }
  ],
  "refresh": "30s",
  "schemaVersion": 38,
  "tags": ["nexspence"],
  "templating": {"list": []},
  "time": {"from": "now-1h", "to": "now"},
  "timepicker": {},
  "timezone": "browser",
  "title": "Nexspence",
  "uid": "nexspence-overview",
  "version": 1
}
```

- [ ] Create `deploy/monitoring/docker-compose.yml` (standalone — uses `host-gateway` so `nexspence:8081` resolves to the host machine, same `prometheus.yml` works for both):
```yaml
# Standalone monitoring stack — connects to a Nexspence instance running on the host.
# On Linux, host-gateway resolves to the Docker bridge IP (usually 172.17.0.1).
# On Mac/Windows Docker Desktop, host-gateway resolves to the host machine's IP.
#
# Setup:
#   printf 'nxs_your_token' > prometheus-token
#   docker compose up -d
#
# Grafana: http://localhost:3000  (admin / admin, anonymous viewer also works)
# Prometheus: http://localhost:9090

services:
  prometheus:
    image: prom/prometheus:v2.51.0
    volumes:
      - ./prometheus.yml:/etc/prometheus/prometheus.yml:ro
      - ./prometheus-token:/etc/prometheus/token:ro
      - prometheus_data:/prometheus
    ports:
      - "9090:9090"
    command:
      - "--config.file=/etc/prometheus/prometheus.yml"
      - "--storage.tsdb.retention.time=15d"
    extra_hosts:
      - "nexspence:host-gateway"

  grafana:
    image: grafana/grafana:10.4.0
    environment:
      GF_SECURITY_ADMIN_PASSWORD: admin
      GF_AUTH_ANONYMOUS_ENABLED: "true"
      GF_AUTH_ANONYMOUS_ORG_ROLE: Viewer
    volumes:
      - ./grafana/provisioning:/etc/grafana/provisioning:ro
      - ./grafana/dashboards:/var/lib/grafana/dashboards:ro
      - grafana_data:/var/lib/grafana
    ports:
      - "3000:3000"
    depends_on:
      - prometheus

volumes:
  prometheus_data:
  grafana_data:
```

- [ ] Add token file to `.gitignore` (append):
```
deploy/monitoring/prometheus-token
```

- [ ] Commit:
```bash
git add deploy/monitoring/ .gitignore
git commit -m "feat(monitoring): add Prometheus config and Grafana auto-provisioned stack"
```

---

## Task 7: Add monitoring profile to main docker-compose.yml

**Files:**
- Modify: `docker-compose.yml`

- [ ] In `docker-compose.yml`, after the `website` service block and before the `volumes:` section, add:
```yaml
  # ── Prometheus + Grafana (profile: monitoring) ────────────────
  # Start: docker compose --profile monitoring up -d prometheus grafana
  # First:  printf 'nxs_your_token' > deploy/monitoring/prometheus-token
  # Grafana:    http://localhost:3000  (admin / admin)
  # Prometheus: http://localhost:9090
  prometheus:
    profiles: [monitoring]
    image: prom/prometheus:v2.51.0
    volumes:
      - ./deploy/monitoring/prometheus.yml:/etc/prometheus/prometheus.yml:ro
      - ./deploy/monitoring/prometheus-token:/etc/prometheus/token:ro
      - prometheus_data:/prometheus
    ports:
      - "9090:9090"
    command:
      - "--config.file=/etc/prometheus/prometheus.yml"
      - "--storage.tsdb.retention.time=15d"
    depends_on:
      nexspence:
        condition: service_healthy

  grafana:
    profiles: [monitoring]
    image: grafana/grafana:10.4.0
    environment:
      GF_SECURITY_ADMIN_PASSWORD: admin
      GF_AUTH_ANONYMOUS_ENABLED: "true"
      GF_AUTH_ANONYMOUS_ORG_ROLE: Viewer
    volumes:
      - ./deploy/monitoring/grafana/provisioning:/etc/grafana/provisioning:ro
      - ./deploy/monitoring/grafana/dashboards:/var/lib/grafana/dashboards:ro
      - grafana_data:/var/lib/grafana
    ports:
      - "3000:3000"
    depends_on:
      - prometheus
```

- [ ] In the `volumes:` section, add:
```yaml
  prometheus_data:
  grafana_data:
```

- [ ] Verify compose syntax:
```bash
docker compose config --quiet
```
Expected: exits 0, no errors.

- [ ] Commit:
```bash
git add docker-compose.yml
git commit -m "feat(monitoring): add monitoring profile to docker-compose"
```

---

## Task 8: Frontend — install recharts and rewrite MonitoringPage

**Files:**
- Modify: `frontend/package.json` (via npm install)
- Modify: `frontend/src/pages/MonitoringPage.tsx`

- [ ] Install recharts:
```bash
cd frontend && npm install recharts
```
Expected: `package.json` now lists `"recharts": "^2.x.x"` in dependencies.

- [ ] Replace `frontend/src/pages/MonitoringPage.tsx` entirely:
```tsx
import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import {
  Activity, BarChart2, Cpu, Database, Download,
  GitBranch, HardDrive, RefreshCw, Trash2, TrendingUp, Upload,
} from 'lucide-react'
import {
  AreaChart, Area, LineChart, Line,
  XAxis, YAxis, CartesianGrid, Tooltip, ResponsiveContainer,
} from 'recharts'
import { nexusApi } from '@/api/client'

// ── Types ──────────────────────────────────────────────────────────────────
interface MemStats { alloc_bytes: number; total_alloc_bytes: number; sys_bytes: number; gc_cycles: number }
interface MetricsSnapshot {
  uptime_seconds: number; requests_total: number; request_errors: number
  artifacts_stored: number; bytes_stored: number; downloads_total: number
  artifacts_deleted: number; goroutines: number; memory: MemStats
}
interface DataPoint {
  timestamp: number; requests_total: number; request_errors: number
  artifacts_stored: number; bytes_stored: number; downloads_total: number; goroutines: number
}
interface RepoMetric { name: string; format: string; type: string; downloads: number; size_bytes: number }
interface ChartPoint { time: string; rps: number; errorRate: number; bytes_stored: number }

// ── Styles ─────────────────────────────────────────────────────────────────
const S = {
  page:      { display: 'flex', flexDirection: 'column' as const, gap: 20 },
  header:    { display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', flexWrap: 'wrap' as const, gap: 12 },
  title:     { fontSize: 20, fontWeight: 700, color: '#dbeafe', margin: '0 0 4px' },
  subtitle:  { fontSize: 13, color: 'rgba(229,231,235,0.5)', margin: 0 },
  tabs:      { display: 'flex', borderBottom: '1px solid rgba(255,255,255,0.08)', marginBottom: 4 },
  tab:       (active: boolean): React.CSSProperties => ({
    padding: '8px 18px', fontSize: 13, fontWeight: 600, cursor: 'pointer',
    background: 'none', border: 'none',
    borderBottom: active ? '2px solid #3b82f6' : '2px solid transparent',
    color: active ? '#dbeafe' : 'rgba(229,231,235,0.45)',
  }),
  grid3:     { display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(200px, 1fr))', gap: 14 },
  grid2:     { display: 'grid', gridTemplateColumns: 'repeat(2, 1fr)', gap: 14 },
  card:      { background: 'rgba(255,255,255,0.03)', border: '1px solid rgba(255,255,255,0.08)', borderRadius: 14, padding: 18 },
  cardTitle: { fontSize: 11, fontWeight: 600, color: 'rgba(229,231,235,0.45)', textTransform: 'uppercase' as const, letterSpacing: '0.06em', marginBottom: 12, display: 'flex', alignItems: 'center', gap: 6 },
  bigNum:    { fontSize: 28, fontWeight: 700, color: '#dbeafe', lineHeight: 1, marginBottom: 4 },
  label:     { fontSize: 12, color: 'rgba(229,231,235,0.45)' },
  row:       { display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '7px 0', borderBottom: '1px solid rgba(255,255,255,0.05)', fontSize: 13 },
  rowKey:    { color: 'rgba(229,231,235,0.5)' },
  rowVal:    { color: '#dbeafe', fontWeight: 600, fontVariantNumeric: 'tabular-nums' as const },
  iconBtn:   { background: 'rgba(255,255,255,0.06)', border: '1px solid rgba(255,255,255,0.1)', borderRadius: 8, padding: 8, color: 'rgba(229,231,235,0.7)', cursor: 'pointer', display: 'flex', alignItems: 'center', gap: 6, fontSize: 13 },
  badge:     (c: string): React.CSSProperties => ({ fontSize: 11, fontWeight: 600, padding: '2px 7px', borderRadius: 4, background: c + '20', color: c }),
  bar:       { height: 6, borderRadius: 3, background: 'rgba(255,255,255,0.07)', overflow: 'hidden' as const, marginTop: 8 },
  sectionHd: { fontSize: 14, fontWeight: 600, color: '#dbeafe', marginBottom: 12, display: 'flex', alignItems: 'center', gap: 8 },
  chartCard: { background: 'rgba(255,255,255,0.03)', border: '1px solid rgba(255,255,255,0.08)', borderRadius: 14, padding: '18px 18px 8px' },
  chartLbl:  { fontSize: 11, fontWeight: 600, color: 'rgba(229,231,235,0.45)', textTransform: 'uppercase' as const, letterSpacing: '0.06em', marginBottom: 16 },
  table:     { width: '100%', borderCollapse: 'collapse' as const, fontSize: 13 },
  th:        { textAlign: 'left' as const, padding: '8px 12px', fontSize: 11, fontWeight: 600, color: 'rgba(229,231,235,0.4)', textTransform: 'uppercase' as const, letterSpacing: '0.06em', borderBottom: '1px solid rgba(255,255,255,0.08)' },
  td:        { padding: '9px 12px', borderBottom: '1px solid rgba(255,255,255,0.05)', color: '#dbeafe' },
}

const tooltipStyle: React.CSSProperties = {
  background: '#0d1526', border: '1px solid rgba(255,255,255,0.12)', borderRadius: 8, fontSize: 12, color: '#dbeafe',
}
const tickStyle = { fill: 'rgba(229,231,235,0.35)', fontSize: 11 }

// ── Helpers ────────────────────────────────────────────────────────────────
function fmtBytes(b: number) {
  if (b < 1024) return b + ' B'
  if (b < 1024 ** 2) return (b / 1024).toFixed(1) + ' KB'
  if (b < 1024 ** 3) return (b / 1024 ** 2).toFixed(1) + ' MB'
  return (b / 1024 ** 3).toFixed(2) + ' GB'
}
function fmtUptime(s: number) {
  const d = Math.floor(s / 86400), h = Math.floor((s % 86400) / 3600), m = Math.floor((s % 3600) / 60)
  if (d > 0) return `${d}d ${h}h ${m}m`
  if (h > 0) return `${h}h ${m}m`
  return `${m}m ${Math.floor(s % 60)}s`
}
function fmtNum(n: number) {
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(1) + 'M'
  if (n >= 1_000) return (n / 1_000).toFixed(1) + 'K'
  return String(n)
}
function processHistory(history: DataPoint[]): ChartPoint[] {
  return history.map((p, i) => {
    const time = new Date(p.timestamp * 1000).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
    if (i === 0) return { time, rps: 0, errorRate: 0, bytes_stored: p.bytes_stored }
    const prev = history[i - 1]
    const dt = Math.max(p.timestamp - prev.timestamp, 1)
    const reqDelta = Math.max(p.requests_total - prev.requests_total, 0)
    const errDelta = Math.max(p.request_errors - prev.request_errors, 0)
    return {
      time,
      rps: parseFloat((reqDelta / dt).toFixed(2)),
      errorRate: parseFloat((reqDelta > 0 ? (errDelta / reqDelta) * 100 : 0).toFixed(2)),
      bytes_stored: p.bytes_stored,
    }
  })
}

// ── StatCard ───────────────────────────────────────────────────────────────
function StatCard({ icon: Icon, color, title, value, sub }: {
  icon: React.ElementType; color: string; title: string; value: string; sub?: string
}) {
  return (
    <div style={S.card}>
      <div style={S.cardTitle}><Icon size={13} style={{ color }} />{title}</div>
      <div style={{ ...S.bigNum, color }}>{value}</div>
      {sub && <div style={S.label}>{sub}</div>}
    </div>
  )
}

// ── Overview tab ───────────────────────────────────────────────────────────
function OverviewTab({ m }: { m: MetricsSnapshot }) {
  const errRate = m.requests_total > 0 ? ((m.request_errors / m.requests_total) * 100).toFixed(1) : '0.0'
  const errColor = m.request_errors > 0 ? '#f59e0b' : '#22c55e'
  const heapPct = Math.min((m.memory.alloc_bytes / m.memory.sys_bytes) * 100, 100)
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
      <div style={S.grid3}>
        <StatCard icon={Activity}   color="#3b82f6" title="Total Requests"    value={fmtNum(m.requests_total)}    sub="since process start" />
        <StatCard icon={Upload}     color="#22c55e" title="Artifacts Stored"  value={fmtNum(m.artifacts_stored)}  sub={fmtBytes(m.bytes_stored) + ' written'} />
        <StatCard icon={Download}   color="#a78bfa" title="Downloads"         value={fmtNum(m.downloads_total)}   sub="artifact fetches" />
        <StatCard icon={Trash2}     color="#f59e0b" title="Artifacts Deleted" value={fmtNum(m.artifacts_deleted)} sub="since process start" />
        <StatCard icon={TrendingUp} color={errColor} title="Request Errors"  value={m.request_errors.toString()} sub={errRate + '% error rate'} />
        <StatCard icon={Cpu}        color="#06b6d4" title="Goroutines"         value={m.goroutines.toString()}     sub={'uptime ' + fmtUptime(m.uptime_seconds)} />
      </div>
      <div style={S.grid2}>
        <div style={S.card}>
          <div style={S.sectionHd}><HardDrive size={15} style={{ color: 'rgba(229,231,235,0.5)' }} />Memory</div>
          <div style={S.row}><span style={S.rowKey}>Heap allocated</span><span style={S.rowVal}>{fmtBytes(m.memory.alloc_bytes)}</span></div>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            <div style={{ ...S.bar, flex: 1 }}>
              <div style={{ height: '100%', width: heapPct + '%', background: heapPct > 80 ? '#ef4444' : '#3b82f6', transition: 'width 0.4s' }} />
            </div>
            <span style={{ fontSize: 11, fontWeight: 600, color: heapPct > 80 ? '#ef4444' : '#3b82f6', whiteSpace: 'nowrap' }}>{heapPct.toFixed(0)}%{heapPct > 80 ? ' HIGH' : ' OK'}</span>
          </div>
          <div style={S.row}><span style={S.rowKey}>Total allocated</span><span style={S.rowVal}>{fmtBytes(m.memory.total_alloc_bytes)}</span></div>
          <div style={S.row}><span style={S.rowKey}>System reserved</span><span style={S.rowVal}>{fmtBytes(m.memory.sys_bytes)}</span></div>
          <div style={{ ...S.row, borderBottom: 'none' }}><span style={S.rowKey}>GC cycles</span><span style={S.rowVal}>{m.memory.gc_cycles}</span></div>
        </div>
        <div style={S.card}>
          <div style={S.sectionHd}><Database size={15} style={{ color: 'rgba(229,231,235,0.5)' }} />Storage Activity</div>
          <div style={S.row}><span style={S.rowKey}>Artifacts stored</span><span style={S.rowVal}>{m.artifacts_stored.toLocaleString()}</span></div>
          <div style={S.row}><span style={S.rowKey}>Total bytes written</span><span style={S.rowVal}>{fmtBytes(m.bytes_stored)}</span></div>
          <div style={S.row}><span style={S.rowKey}>Downloads served</span><span style={S.rowVal}>{m.downloads_total.toLocaleString()}</span></div>
          <div style={S.row}><span style={S.rowKey}>Artifacts deleted</span><span style={S.rowVal}>{(m.artifacts_deleted ?? 0).toLocaleString()}</span></div>
          <div style={{ ...S.row, borderBottom: 'none' }}>
            <span style={S.rowKey}>Error rate</span>
            <span style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
              <span style={S.badge(errColor)}>{errRate}%</span>
              <span style={{ fontSize: 11, color: errColor, fontWeight: 600 }}>{m.request_errors > 0 ? 'WARN' : 'OK'}</span>
            </span>
          </div>
        </div>
      </div>
    </div>
  )
}

// ── Charts tab ─────────────────────────────────────────────────────────────
function ChartsTab() {
  const { data: history = [] } = useQuery<DataPoint[]>({
    queryKey: ['metrics-history'],
    queryFn: () => nexusApi.get('/api/v1/metrics/history').then(r => r.data),
    refetchInterval: 30_000,
  })
  const pts = processHistory(history)
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
      <div style={S.chartCard}>
        <div style={S.chartLbl}>Requests / sec — last 1 hour</div>
        <ResponsiveContainer width="100%" height={200}>
          <AreaChart data={pts}>
            <defs>
              <linearGradient id="g-rps" x1="0" y1="0" x2="0" y2="1">
                <stop offset="5%" stopColor="#3b82f6" stopOpacity={0.3}/>
                <stop offset="95%" stopColor="#3b82f6" stopOpacity={0}/>
              </linearGradient>
            </defs>
            <CartesianGrid strokeDasharray="3 3" stroke="rgba(255,255,255,0.05)" />
            <XAxis dataKey="time" tick={tickStyle} interval="preserveStartEnd" />
            <YAxis tick={tickStyle} width={45} />
            <Tooltip contentStyle={tooltipStyle} formatter={(v: number) => [v.toFixed(2) + ' req/s', 'RPS']} />
            <Area type="monotone" dataKey="rps" stroke="#3b82f6" fill="url(#g-rps)" strokeWidth={1.5} dot={false} />
          </AreaChart>
        </ResponsiveContainer>
      </div>
      <div style={S.chartCard}>
        <div style={S.chartLbl}>Error Rate % — last 1 hour</div>
        <ResponsiveContainer width="100%" height={180}>
          <LineChart data={pts}>
            <CartesianGrid strokeDasharray="3 3" stroke="rgba(255,255,255,0.05)" />
            <XAxis dataKey="time" tick={tickStyle} interval="preserveStartEnd" />
            <YAxis tick={tickStyle} width={45} unit="%" domain={[0, 'auto']} />
            <Tooltip contentStyle={tooltipStyle} formatter={(v: number) => [v.toFixed(2) + '%', 'Error rate']} />
            <Line type="monotone" dataKey="errorRate" stroke="#f59e0b" strokeWidth={1.5} dot={false} />
          </LineChart>
        </ResponsiveContainer>
      </div>
      <div style={S.chartCard}>
        <div style={S.chartLbl}>Storage Growth — last 1 hour</div>
        <ResponsiveContainer width="100%" height={180}>
          <AreaChart data={pts}>
            <defs>
              <linearGradient id="g-store" x1="0" y1="0" x2="0" y2="1">
                <stop offset="5%" stopColor="#22c55e" stopOpacity={0.3}/>
                <stop offset="95%" stopColor="#22c55e" stopOpacity={0}/>
              </linearGradient>
            </defs>
            <CartesianGrid strokeDasharray="3 3" stroke="rgba(255,255,255,0.05)" />
            <XAxis dataKey="time" tick={tickStyle} interval="preserveStartEnd" />
            <YAxis tick={tickStyle} width={65} tickFormatter={fmtBytes} />
            <Tooltip contentStyle={tooltipStyle} formatter={(v: number) => [fmtBytes(v), 'Stored']} />
            <Area type="monotone" dataKey="bytes_stored" stroke="#22c55e" fill="url(#g-store)" strokeWidth={1.5} dot={false} />
          </AreaChart>
        </ResponsiveContainer>
      </div>
    </div>
  )
}

// ── Repositories tab ───────────────────────────────────────────────────────
function ReposTab() {
  const [sortBy, setSortBy] = useState<'downloads' | 'size'>('downloads')
  const { data: repos = [] } = useQuery<RepoMetric[]>({
    queryKey: ['metrics-repos'],
    queryFn: () => nexusApi.get('/api/v1/metrics/repos').then(r => r.data),
    refetchInterval: 60_000,
  })
  const sorted = [...repos].sort((a, b) =>
    sortBy === 'downloads' ? b.downloads - a.downloads : b.size_bytes - a.size_bytes
  )
  return (
    <div style={S.card}>
      <div style={{ ...S.sectionHd, justifyContent: 'space-between' }}>
        <span style={{ display: 'flex', alignItems: 'center', gap: 8 }}><GitBranch size={15} />Top 10 Repositories</span>
        <div style={{ display: 'flex', gap: 6 }}>
          {(['downloads', 'size'] as const).map(k => (
            <button key={k} style={{ ...S.iconBtn, ...(sortBy === k ? { background: 'rgba(59,130,246,0.2)' } : {}) }}
              onClick={() => setSortBy(k)}>By {k === 'downloads' ? 'Downloads' : 'Size'}</button>
          ))}
        </div>
      </div>
      <table style={S.table}>
        <thead>
          <tr>
            <th style={S.th}>Repository</th>
            <th style={S.th}>Format</th>
            <th style={S.th}>Type</th>
            <th style={{ ...S.th, textAlign: 'right' }}>Downloads</th>
            <th style={{ ...S.th, textAlign: 'right' }}>Size</th>
          </tr>
        </thead>
        <tbody>
          {sorted.map(r => (
            <tr key={r.name}>
              <td style={S.td}>{r.name}</td>
              <td style={S.td}>
                <span style={{ fontSize: 10, fontWeight: 600, padding: '2px 6px', borderRadius: 4, background: 'rgba(59,130,246,0.15)', color: '#93c5fd' }}>{r.format}</span>
              </td>
              <td style={{ ...S.td, color: 'rgba(229,231,235,0.5)' }}>{r.type}</td>
              <td style={{ ...S.td, textAlign: 'right', fontVariantNumeric: 'tabular-nums' }}>{r.downloads.toLocaleString()}</td>
              <td style={{ ...S.td, textAlign: 'right', fontVariantNumeric: 'tabular-nums' }}>{fmtBytes(r.size_bytes)}</td>
            </tr>
          ))}
          {sorted.length === 0 && (
            <tr><td colSpan={5} style={{ ...S.td, textAlign: 'center', color: 'rgba(229,231,235,0.3)', padding: 24 }}>No repositories yet</td></tr>
          )}
        </tbody>
      </table>
    </div>
  )
}

// ── Main export ────────────────────────────────────────────────────────────
export function MonitoringView() {
  const [tab, setTab] = useState<'overview' | 'charts' | 'repos'>('overview')
  const { data: m, isLoading, dataUpdatedAt, refetch } = useQuery<MetricsSnapshot>({
    queryKey: ['metrics'],
    queryFn: () => nexusApi.getMetrics().then(r => r.data),
    refetchInterval: 10_000,
  })
  const lastUpdate = dataUpdatedAt ? new Date(dataUpdatedAt).toLocaleTimeString() : '—'

  return (
    <div style={S.page}>
      <div style={S.header}>
        <div>
          <h1 style={S.title}>Monitoring</h1>
          <p style={S.subtitle}>Live metrics — auto-refreshes every 10 s</p>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
          <span style={{ fontSize: 12, color: 'rgba(229,231,235,0.4)' }}>Updated {lastUpdate}</span>
          <button style={S.iconBtn} onClick={() => refetch()} title="Refresh now"><RefreshCw size={14} /></button>
        </div>
      </div>

      <div style={S.tabs}>
        <button style={S.tab(tab === 'overview')} onClick={() => setTab('overview')}>
          <Activity size={12} style={{ display: 'inline', marginRight: 5 }} />Overview
        </button>
        <button style={S.tab(tab === 'charts')} onClick={() => setTab('charts')}>
          <BarChart2 size={12} style={{ display: 'inline', marginRight: 5 }} />Charts
        </button>
        <button style={S.tab(tab === 'repos')} onClick={() => setTab('repos')}>
          <Database size={12} style={{ display: 'inline', marginRight: 5 }} />Repositories
        </button>
      </div>

      {isLoading ? (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
          <div className="holo-skeleton holo-skeleton--block" />
          <div className="holo-skeleton holo-skeleton--block" />
        </div>
      ) : !m ? (
        <p style={{ color: 'rgba(239,68,68,0.7)', fontSize: 14 }}>Failed to load metrics</p>
      ) : (
        <>
          {tab === 'overview' && <OverviewTab m={m} />}
          {tab === 'charts'   && <ChartsTab />}
          {tab === 'repos'    && <ReposTab />}
        </>
      )}
    </div>
  )
}
```

- [ ] Build frontend to verify TypeScript compiles:
```bash
cd frontend && npm run build 2>&1 | tail -20
```
Expected: build succeeds, no TypeScript errors.

- [ ] Commit:
```bash
git add frontend/package.json frontend/package-lock.json frontend/src/pages/MonitoringPage.tsx
git commit -m "feat(ui): extend MonitoringPage with tabs, time-series charts, and repos table"
```

---

## Task 9: Update NEXT_RELEASE.md and final verification

**Files:** `NEXT_RELEASE.md`

- [ ] Append to `NEXT_RELEASE.md` under `### ✨ Features`:
```markdown
- **Extended Monitoring — Prometheus endpoint**: `GET /metrics` (Bearer auth required) exposes 8 metrics: requests total/rate, request duration histogram, artifacts count, bytes stored, downloads, goroutines, memory alloc. Background sampler updates gauges every 10 s.
- **Extended Monitoring — Grafana stack**: `docker compose --profile monitoring up` starts Prometheus + Grafana with pre-provisioned datasource and 8-panel dashboard (RPS, error rate, latency p95, storage, downloads/min, artifacts, goroutines, memory). Standalone install: `cd deploy/monitoring && docker compose up`.
- **Extended Monitoring — UI Dashboard**: MonitoringPage extended with 3 tabs — Overview (existing stat cards), Charts (AreaChart/LineChart for RPS, error rate %, storage growth via recharts, 1h history from backend ring buffer), Repositories (top 10 repos by downloads or size).
```

- [ ] Run full Go test suite:
```bash
go test ./... 2>&1 | tail -10
```
Expected: all tests pass (465+ tests).

- [ ] Run frontend build:
```bash
cd frontend && npm run build 2>&1 | tail -5
```
Expected: no errors.

- [ ] Commit:
```bash
git add NEXT_RELEASE.md
git commit -m "chore: update NEXT_RELEASE.md for extended monitoring"
```
