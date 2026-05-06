package metrics

import (
	"runtime"
	"sync/atomic"
	"time"
)

var (
	RequestsTotal    atomic.Int64
	RequestErrors    atomic.Int64
	AuditEventsCount atomic.Int64

	startTime = time.Now()
)

// Snapshot returns a point-in-time snapshot of session and runtime metrics.
func Snapshot() Map {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	return Map{
		"uptime_seconds":     time.Since(startTime).Seconds(),
		"requests_total":     RequestsTotal.Load(),
		"request_errors":     RequestErrors.Load(),
		"audit_events_count": AuditEventsCount.Load(),
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
