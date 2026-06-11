package service

import (
	"context"
	"sync"
	"time"

	"github.com/nexspence-oss/nexspence/internal/logger"
)

// DownloadFlusher is the narrow slice of repository.AssetRepo the counter needs.
type DownloadFlusher interface {
	IncrementDownloads(ctx context.Context, counts map[string]int64) error
}

// DownloadCounter aggregates download-count increments in memory and flushes
// them to the database in periodic batches. Counts pending at shutdown or
// crash are lost by design — acceptable for download statistics.
type DownloadCounter struct {
	assets DownloadFlusher
	log    logger.Logger

	mu      sync.Mutex
	pending map[string]int64
}

// NewDownloadCounter constructs a counter flushing through assets.
func NewDownloadCounter(assets DownloadFlusher, log logger.Logger) *DownloadCounter {
	return &DownloadCounter{
		assets:  assets,
		log:     log,
		pending: make(map[string]int64),
	}
}

// Add records one download for assetID. Non-blocking; safe for concurrent use.
func (c *DownloadCounter) Add(assetID string) {
	c.mu.Lock()
	c.pending[assetID]++
	c.mu.Unlock()
}

// Start flushes every interval until ctx is canceled (one final flush on exit).
// Run as a goroutine.
func (c *DownloadCounter) Start(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			c.Flush(context.Background())
		case <-ctx.Done():
			c.Flush(context.Background())
			return
		}
	}
}

// Flush writes the pending batch. On error the batch is dropped (logged) so a
// dead database cannot grow the map without bound.
func (c *DownloadCounter) Flush(ctx context.Context) {
	c.mu.Lock()
	batch := c.pending
	c.pending = make(map[string]int64)
	c.mu.Unlock()
	if len(batch) == 0 {
		return
	}
	if err := c.assets.IncrementDownloads(ctx, batch); err != nil {
		c.log.Warn("download counter: flush failed, dropping batch", "assets", len(batch), "err", err)
	}
}
