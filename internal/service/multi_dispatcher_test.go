package service

import (
	"sync/atomic"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/domain"
)

type countingDispatcher struct{ n atomic.Int64 }

func (c *countingDispatcher) Dispatch(_ domain.WebhookPayload) { c.n.Add(1) }

func TestMultiDispatcher_FansOutToAllTargets(t *testing.T) {
	a := &countingDispatcher{}
	b := &countingDispatcher{}
	md := NewMultiDispatcher(a, nil, b) // nil is tolerated

	md.Dispatch(domain.WebhookPayload{Event: domain.EventArtifactPublished})
	md.Dispatch(domain.WebhookPayload{Event: domain.EventProxyError})

	if got := a.n.Load(); got != 2 {
		t.Fatalf("target A got %d dispatches, want 2", got)
	}
	if got := b.n.Load(); got != 2 {
		t.Fatalf("target B got %d dispatches, want 2", got)
	}
}
