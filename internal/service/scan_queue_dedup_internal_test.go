package service

import (
	"context"
	"testing"
)

// A trigger for a component already waiting in the automatic queue is
// dropped — the scan still to come reads the component as it is by then — and
// once the worker picks it up, a new trigger queues a new scan (#542: a rule
// waiting for a scan re-asks for it on every re-check).
func TestScanService_TriggerAsyncSkipsQueuedDuplicates(t *testing.T) {
	s := NewScanService(nil, "")
	s.TriggerAsync("a")
	s.TriggerAsync("a")
	s.TriggerAsync("b")
	if got := len(s.queue); got != 2 {
		t.Fatalf("queued %d, want a and b once each", got)
	}
	id := <-s.queue
	s.dequeued(id)
	s.TriggerAsync(id)
	if got := len(s.queue); got != 2 {
		t.Fatalf("queued %d after the pick-up, want the re-trigger queued", got)
	}
}

func TestScanService_CoversFormat(t *testing.T) {
	s := NewScanService(nil, "") // Trivy not enabled
	ctx := context.Background()
	if ok, why := s.CoversFormat(ctx, "maven2"); !ok || why != "" {
		t.Fatalf("maven2 = %v, %q", ok, why)
	}
	if ok, why := s.CoversFormat(ctx, "raw"); ok || why != "no scanner covers raw components" {
		t.Fatalf("raw = %v, %q", ok, why)
	}
	if ok, why := s.CoversFormat(ctx, "docker"); ok || why == "" {
		t.Fatalf("docker without Trivy = %v, %q", ok, why)
	}
}
