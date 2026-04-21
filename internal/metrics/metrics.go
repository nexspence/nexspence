// Package metrics holds in-process atomic counters readable by any package.
// No dependencies on internal packages — safe to import from anywhere.
package metrics

import (
	"runtime"
	"sync/atomic"
	"time"
)

var (
	RequestsTotal    atomic.Int64
	RequestErrors    atomic.Int64
	ArtifactsStored  atomic.Int64
	BytesStored      atomic.Int64
	DownloadsTotal   atomic.Int64
	ArtifactsDeleted atomic.Int64

	startTime = time.Now()
)

// Snapshot returns a point-in-time snapshot of all metrics.
func Snapshot() Map {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	return Map{
		"uptime_seconds":   time.Since(startTime).Seconds(),
		"requests_total":    RequestsTotal.Load(),
		"request_errors":    RequestErrors.Load(),
		"artifacts_stored":  ArtifactsStored.Load(),
		"bytes_stored":      BytesStored.Load(),
		"downloads_total":   DownloadsTotal.Load(),
		"artifacts_deleted": ArtifactsDeleted.Load(),
		"goroutines":       runtime.NumGoroutine(),
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
