// Package events implements an in-memory pub/sub broker used to push
// realtime events (artifact pushes, deletes, proxy errors, ...) to
// SSE/WebSocket clients without the cost of polling.
package events

import (
	"sync"
	"sync/atomic"

	"github.com/nexspence-oss/nexspence/internal/domain"
)

// Subscription is a per-client channel of events.
// The channel is buffered; on overflow the broker drops the new event for
// that subscriber and increments DroppedCount, instead of blocking publishers.
type Subscription struct {
	C            <-chan domain.WebhookPayload
	out          chan domain.WebhookPayload
	DroppedCount uint64
}

// Broker fans out webhook payloads to any number of in-process subscribers.
// It also satisfies domain.WebhookDispatcher so it can be plugged into the
// same call sites as WebhookService (via a tee — see service.MultiDispatcher).
type Broker struct {
	mu     sync.RWMutex
	subs   map[*Subscription]struct{}
	bufLen int
}

// NewBroker creates a broker; bufLen is the per-subscriber channel buffer.
// 32 is a reasonable default for SSE clients (UI does not need every event).
func NewBroker(bufLen int) *Broker {
	if bufLen <= 0 {
		bufLen = 32
	}
	return &Broker{
		subs:   make(map[*Subscription]struct{}),
		bufLen: bufLen,
	}
}

// Subscribe registers a new subscriber. The caller must call Unsubscribe
// when done (typically via defer when the SSE handler exits).
func (b *Broker) Subscribe() *Subscription {
	ch := make(chan domain.WebhookPayload, b.bufLen)
	s := &Subscription{C: ch, out: ch}
	b.mu.Lock()
	b.subs[s] = struct{}{}
	b.mu.Unlock()
	return s
}

// Unsubscribe removes the subscription and closes its channel.
func (b *Broker) Unsubscribe(s *Subscription) {
	if s == nil {
		return
	}
	b.mu.Lock()
	if _, ok := b.subs[s]; ok {
		delete(b.subs, s)
		close(s.out)
	}
	b.mu.Unlock()
}

// Dispatch broadcasts the payload to every active subscriber.
// Implements domain.WebhookDispatcher.
func (b *Broker) Dispatch(payload domain.WebhookPayload) {
	b.mu.RLock()
	subs := make([]*Subscription, 0, len(b.subs))
	for s := range b.subs {
		subs = append(subs, s)
	}
	b.mu.RUnlock()
	for _, s := range subs {
		select {
		case s.out <- payload:
		default:
			atomic.AddUint64(&s.DroppedCount, 1)
		}
	}
}

// Count returns the current number of active subscribers.
func (b *Broker) Count() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs)
}
