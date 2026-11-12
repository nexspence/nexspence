package repoproxy

import (
	"sync"
	"sync/atomic"
)

const clientTeeBufferBytes = 4 << 20

// Atomic override keeps small-buffer tests independent of concurrent readers.
var clientTeeBufferOverride atomic.Int64

func effectiveClientTeeBufferBytes() int {
	if n := clientTeeBufferOverride.Load(); n > 0 {
		return int(n)
	}
	return clientTeeBufferBytes
}

// A fill owns one bounded tee. With the host inflight limit, queued fill data
// uses at most clientTeeBufferBytes * max_inflight_per_host bytes per host for
// active fills. Completed fills can retain another bounded queue per slow
// response until its handler drains it; this is not a global response-memory cap.
// The consumer removes chunks before writing to the client, so one additional
// copy chunk can be in the consumer's hands.
type clientTee struct {
	mu               sync.Mutex
	chunks           [][]byte
	queued, capacity int
	closed, lagging  bool
	sent             int64
	changed          chan struct{}
}

func newClientTee(capacity int) *clientTee {
	return &clientTee{capacity: capacity, changed: make(chan struct{}, 1)}
}
func (t *clientTee) signal() {
	select {
	case t.changed <- struct{}{}:
	default:
	}
}
func (t *clientTee) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return len(p), nil
	}
	if len(p) > t.capacity-t.queued {
		t.lagging, t.closed = true, true
		t.signal()
		return len(p), nil
	}
	t.chunks = append(t.chunks, append([]byte(nil), p...))
	t.queued += len(p)
	t.sent += int64(len(p))
	t.signal()
	return len(p), nil
}
func (t *clientTee) close() { t.mu.Lock(); t.closed = true; t.signal(); t.mu.Unlock() }
func (t *clientTee) next() ([]byte, bool) {
	for {
		t.mu.Lock()
		if len(t.chunks) > 0 {
			p := t.chunks[0]
			t.chunks[0] = nil
			t.chunks = t.chunks[1:]
			t.queued -= len(p)
			t.mu.Unlock()
			return p, true
		}
		closed := t.closed
		t.mu.Unlock()
		if closed {
			return nil, false
		}
		<-t.changed
	}
}
func (t *clientTee) progress() (bool, int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lagging, t.sent
}
