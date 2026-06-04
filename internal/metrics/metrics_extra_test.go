package metrics_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/nexspence-oss/nexspence/internal/metrics"
)

func TestRecordRequest_DoesNotPanic(t *testing.T) {
	// RecordRequest calls Prometheus counter/histogram — just verify no panic.
	assert.NotPanics(t, func() {
		metrics.RecordRequest("GET", "2xx", 15*time.Millisecond)
		metrics.RecordRequest("POST", "5xx", 200*time.Millisecond)
	})
}

func TestUpdateGauges_DoesNotPanic(t *testing.T) {
	assert.NotPanics(t, func() {
		metrics.UpdateGauges(100, 1024*1024, 42)
	})
}
