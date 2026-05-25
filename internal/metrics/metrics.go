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
