package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/events"
)

func init() { gin.SetMode(gin.TestMode) }

// flushRecorder is an httptest.ResponseRecorder that also implements http.Flusher
// (the real ResponseRecorder doesn't, so SSE handler would 500 without it).
type flushRecorder struct {
	*httptest.ResponseRecorder
	flushed chan struct{}
	once    sync.Once
}

func newFlushRecorder() *flushRecorder {
	return &flushRecorder{ResponseRecorder: httptest.NewRecorder(), flushed: make(chan struct{})}
}

func (f *flushRecorder) Flush() {
	f.once.Do(func() { close(f.flushed) })
}

func TestEventsHandler_StreamsPublishedPayload(t *testing.T) {
	broker := events.NewBroker(8)
	h := NewEventsHandler(broker)
	h.keepAlive = time.Hour // disable keepalive in this test

	w := newFlushRecorder()
	c, _ := gin.CreateTestContext(w)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/events", nil).WithContext(ctx)

	done := make(chan struct{})
	go func() {
		h.Stream(c)
		close(done)
	}()

	// Wait until the handler has subscribed (otherwise Dispatch races
	// the Subscribe and the event is silently dropped).
	deadline := time.Now().Add(time.Second)
	for broker.Count() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if broker.Count() == 0 {
		t.Fatal("subscriber never registered")
	}

	broker.Dispatch(domain.WebhookPayload{
		Event:      domain.EventArtifactPublished,
		Repository: "raw1",
	})

	// Give the writer a moment, then cancel to unblock the handler.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handler did not exit after context cancel")
	}

	body := w.Body.String()
	if !strings.Contains(body, "event: artifact.published") {
		t.Fatalf("body missing event header: %q", body)
	}
	if !strings.Contains(body, `"repository":"raw1"`) {
		t.Fatalf("body missing payload data: %q", body)
	}
	if !strings.Contains(body, ": connected") {
		t.Fatalf("body missing connection comment: %q", body)
	}
}

func TestEventsHandler_FilterByEvent(t *testing.T) {
	broker := events.NewBroker(8)
	h := NewEventsHandler(broker)
	h.keepAlive = time.Hour

	w := newFlushRecorder()
	c, _ := gin.CreateTestContext(w)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/events?event=proxy.error", nil).WithContext(ctx)

	done := make(chan struct{})
	go func() {
		h.Stream(c)
		close(done)
	}()

	deadline := time.Now().Add(time.Second)
	for broker.Count() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	// Only proxy.error should appear; artifact.published is filtered out.
	broker.Dispatch(domain.WebhookPayload{Event: domain.EventArtifactPublished, Repository: "skipme"})
	broker.Dispatch(domain.WebhookPayload{Event: domain.EventProxyError, Repository: "keep"})

	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	body := w.Body.String()
	if strings.Contains(body, "skipme") {
		t.Fatalf("filtered event leaked: %q", body)
	}
	if !strings.Contains(body, "event: proxy.error") || !strings.Contains(body, `"repository":"keep"`) {
		t.Fatalf("expected event missing: %q", body)
	}
}

func TestEventsHandler_NoBrokerReturns503(t *testing.T) {
	h := NewEventsHandler(nil)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/events", nil)
	h.Stream(c)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}
