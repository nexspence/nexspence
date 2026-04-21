package events

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/nexspence-oss/nexspence/internal/domain"
)

func mkPayload(ev domain.WebhookEvent, repo string) domain.WebhookPayload {
	return domain.WebhookPayload{Event: ev, Repository: repo, Timestamp: time.Unix(0, 0)}
}

func TestBroker_PublishReachesAllSubscribers(t *testing.T) {
	b := NewBroker(4)
	s1 := b.Subscribe()
	s2 := b.Subscribe()
	defer b.Unsubscribe(s1)
	defer b.Unsubscribe(s2)

	b.Dispatch(mkPayload(domain.EventArtifactPublished, "r1"))

	for i, s := range []*Subscription{s1, s2} {
		select {
		case ev := <-s.C:
			if ev.Event != domain.EventArtifactPublished || ev.Repository != "r1" {
				t.Fatalf("sub %d got wrong payload: %+v", i, ev)
			}
		case <-time.After(time.Second):
			t.Fatalf("sub %d did not receive event", i)
		}
	}
}

func TestBroker_UnsubscribeStopsDelivery(t *testing.T) {
	b := NewBroker(4)
	s := b.Subscribe()
	b.Unsubscribe(s)

	// Subsequent dispatch must not panic and must not block on a closed chan.
	b.Dispatch(mkPayload(domain.EventArtifactDeleted, "r2"))

	// Channel is closed after Unsubscribe; draining must terminate.
	select {
	case _, ok := <-s.C:
		if ok {
			t.Fatalf("closed sub channel unexpectedly delivered a value")
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatalf("read from closed channel blocked")
	}

	if b.Count() != 0 {
		t.Fatalf("subscriber count = %d, want 0", b.Count())
	}
}

func TestBroker_OverflowDropsWithoutBlocking(t *testing.T) {
	b := NewBroker(2) // small buffer, easy to overflow
	s := b.Subscribe()
	defer b.Unsubscribe(s)

	// 10 dispatches, only 2 fit in the buffer — the rest must be dropped
	// rather than blocking the publisher.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 10; i++ {
			b.Dispatch(mkPayload(domain.EventArtifactPublished, "r"))
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("Dispatch blocked on full subscriber buffer")
	}

	if atomic.LoadUint64(&s.DroppedCount) == 0 {
		t.Fatalf("expected DroppedCount > 0 after overflow, got 0")
	}
}

func TestBroker_NilTargetTolerated(t *testing.T) {
	// Smoke test: Dispatch on a broker with no subscribers.
	b := NewBroker(0) // exercises default bufLen
	b.Dispatch(mkPayload(domain.EventRepoCreated, ""))
	if b.Count() != 0 {
		t.Fatalf("Count() should be 0 with no subscribers")
	}
}
