package service_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/nexspence-oss/nexspence/internal/service"
)

type fakeFlusher struct {
	mu    sync.Mutex
	calls []map[string]int64
	err   error
}

func (f *fakeFlusher) IncrementDownloads(_ context.Context, counts map[string]int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make(map[string]int64, len(counts))
	for k, v := range counts {
		cp[k] = v
	}
	f.calls = append(f.calls, cp)
	return f.err
}

func (f *fakeFlusher) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func TestDownloadCounter_FlushAggregatesAndClears(t *testing.T) {
	fl := &fakeFlusher{}
	c := service.NewDownloadCounter(fl, zap.NewNop().Sugar())

	c.Add("a1")
	c.Add("a1")
	c.Add("a2")
	c.Flush(context.Background())

	if got := fl.callCount(); got != 1 {
		t.Fatalf("flush calls: got %d want 1", got)
	}
	if fl.calls[0]["a1"] != 2 || fl.calls[0]["a2"] != 1 {
		t.Fatalf("flushed batch: got %v", fl.calls[0])
	}

	// Nothing pending → no second DB call.
	c.Flush(context.Background())
	if got := fl.callCount(); got != 1 {
		t.Fatalf("empty flush must not call repo: got %d calls", got)
	}
}

func TestDownloadCounter_FlushErrorDropsBatch(t *testing.T) {
	fl := &fakeFlusher{err: errors.New("db down")}
	c := service.NewDownloadCounter(fl, zap.NewNop().Sugar())

	c.Add("a1")
	c.Flush(context.Background()) // error logged, batch dropped by design

	fl.err = nil
	c.Flush(context.Background())
	if got := fl.callCount(); got != 1 {
		t.Fatalf("dropped batch must not be retried: got %d calls", got)
	}
}

func TestDownloadCounter_StartFlushesOnTickerAndStopsOnCancel(t *testing.T) {
	fl := &fakeFlusher{}
	c := service.NewDownloadCounter(fl, zap.NewNop().Sugar())
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() { c.Start(ctx, 10*time.Millisecond); close(done) }()

	c.Add("a1")
	deadline := time.Now().Add(2 * time.Second)
	for fl.callCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if fl.callCount() == 0 {
		t.Fatal("ticker flush never happened")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not return after context cancel")
	}
}
