package metrics_test

import (
	"testing"

	"github.com/nexspence-oss/nexspence/internal/metrics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSnapshot_ContainsExpectedKeys(t *testing.T) {
	snap := metrics.Snapshot()

	topKeys := []string{
		"uptime_seconds",
		"requests_total",
		"request_errors",
		"audit_events_count",
		"goroutines",
		"memory",
	}
	for _, k := range topKeys {
		assert.Contains(t, snap, k, "snapshot missing key %q", k)
	}

	mem, ok := snap["memory"].(metrics.Map)
	require.True(t, ok, "memory should be a Map")
	memKeys := []string{"alloc_bytes", "total_alloc_bytes", "sys_bytes", "gc_cycles"}
	for _, k := range memKeys {
		assert.Contains(t, mem, k, "memory snapshot missing key %q", k)
	}
}

func TestSnapshot_UptimePositive(t *testing.T) {
	snap := metrics.Snapshot()
	uptime, ok := snap["uptime_seconds"].(float64)
	require.True(t, ok)
	assert.Greater(t, uptime, 0.0)
}

func TestCounters_Requests(t *testing.T) {
	before := metrics.RequestsTotal.Load()
	metrics.RequestsTotal.Add(3)
	snap := metrics.Snapshot()
	assert.Equal(t, int64(before+3), snap["requests_total"])
	metrics.RequestsTotal.Add(-3)
}
