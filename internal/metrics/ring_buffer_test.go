package metrics_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/nexspence-oss/nexspence/internal/metrics"
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
	assert.Equal(t, int64(40), snap[0].Timestamp)    // oldest = 400-360=40
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
