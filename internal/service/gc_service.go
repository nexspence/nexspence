package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/robfig/cron/v3"
	"go.uber.org/zap"

	"github.com/nexspence-oss/nexspence/internal/distlock"
	"github.com/nexspence-oss/nexspence/internal/logger"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/storage"
)

// StoreResolver resolves a physical BlobStore from a descriptor.
// *storage.Registry satisfies this interface.
type StoreResolver interface {
	Get(ctx context.Context, desc storage.BlobStoreDescriptor) (storage.BlobStore, error)
}

// GCOptions controls a compaction run.
// MinAge <= 0 means "use the service's DefaultMinAge".
type GCOptions struct {
	DryRun bool
	MinAge time.Duration
}

// GCResult reports what a single store's compaction found and removed.
type GCResult struct {
	Store        string   `json:"store"`
	ScannedBlobs int      `json:"scannedBlobs"`
	Orphans      int      `json:"orphans"`
	FreedBytes   int64    `json:"freedBytes"`
	DryRun       bool     `json:"dryRun"`
	Errors       []string `json:"errors,omitempty"`
}

// BlobGCService finds and removes blobs not referenced by any asset (orphans),
// age-gated by a grace period, across one or all blob stores.
type BlobGCService struct {
	Assets        repository.AssetRepo
	Stores        repository.BlobStoreRepo
	Resolver      StoreResolver
	Locker        distlock.Locker
	Log           logger.Logger
	DefaultMinAge time.Duration
}

const gcLockKey = "nexspence:lock:gc:run"
const gcLockTTL = 30 * time.Minute

func (s *BlobGCService) log() logger.Logger {
	if s.Log != nil {
		return s.Log
	}
	return zap.NewNop().Sugar()
}

// referencedSet returns the set of all blob keys referenced by any asset.
func (s *BlobGCService) referencedSet(ctx context.Context) (map[string]struct{}, error) {
	keys, err := s.Assets.ListAllBlobKeys(ctx)
	if err != nil {
		return nil, fmt.Errorf("list db blob keys: %w", err)
	}
	set := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		set[k] = struct{}{}
	}
	return set, nil
}

// CompactStore compacts a single blob store by name.
func (s *BlobGCService) CompactStore(ctx context.Context, name string, opts GCOptions) (*GCResult, error) {
	referenced, err := s.referencedSet(ctx)
	if err != nil {
		return nil, err
	}
	row, err := s.Stores.Get(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("get blob store %q: %w", name, err)
	}
	store, err := s.Resolver.Get(ctx, storage.BlobStoreDescriptor{
		ID: row.ID, Type: row.Type, Config: row.Config,
	})
	if err != nil {
		return nil, fmt.Errorf("resolve blob store %q: %w", name, err)
	}
	return s.compact(ctx, name, store, referenced, opts), nil
}

// CompactAll compacts every blob store. It holds a distributed lock so only one
// node runs at a time; if another node holds it, CompactAll returns (nil, nil).
func (s *BlobGCService) CompactAll(ctx context.Context, opts GCOptions) ([]*GCResult, error) {
	if s.Locker != nil {
		lock, err := s.Locker.Acquire(ctx, gcLockKey, gcLockTTL)
		if errors.Is(err, distlock.ErrLockHeld) {
			s.log().Info("blob gc skipped: another node is running gc")
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("blob gc: acquire lock: %w", err)
		}
		defer func() { _ = lock.Release(ctx) }()
	}

	referenced, err := s.referencedSet(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.Stores.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("blob gc: list stores: %w", err)
	}

	results := make([]*GCResult, 0, len(rows))
	for i := range rows {
		row := rows[i]
		store, rerr := s.Resolver.Get(ctx, storage.BlobStoreDescriptor{
			ID: row.ID, Type: row.Type, Config: row.Config,
		})
		if rerr != nil {
			s.log().Error("blob gc: resolve store failed", "store", row.Name, "err", rerr)
			results = append(results, &GCResult{
				Store:  row.Name,
				DryRun: opts.DryRun,
				Errors: []string{fmt.Sprintf("resolve store: %v", rerr)},
			})
			continue
		}
		results = append(results, s.compact(ctx, row.Name, store, referenced, opts))
	}
	return results, nil
}

// compact runs the core scan/delete for a single resolved store.
func (s *BlobGCService) compact(ctx context.Context, name string, store storage.BlobStore,
	referenced map[string]struct{}, opts GCOptions) *GCResult {

	minAge := opts.MinAge
	if minAge <= 0 {
		minAge = s.DefaultMinAge
	}

	result := &GCResult{Store: name, DryRun: opts.DryRun}

	entries, err := store.ListEntries(ctx)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("list entries: %v", err))
		return result
	}
	result.ScannedBlobs = len(entries)

	for _, e := range entries {
		if _, ok := referenced[e.Key]; ok {
			continue // still referenced
		}
		// Age gate: skip blobs younger than the grace period (may be an
		// in-flight upload whose asset row is not committed yet).
		if minAge > 0 && !e.ModTime.IsZero() && time.Since(e.ModTime) < minAge {
			continue
		}
		result.Orphans++
		result.FreedBytes += e.Size
		if !opts.DryRun {
			if derr := store.Delete(ctx, e.Key); derr != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("delete %s: %v", e.Key, derr))
			}
		}
	}
	return result
}

// StartCronScheduler runs CompactAll on the given cron schedule until ctx is
// done. Run as a goroutine. A blank or invalid schedule disables scheduling.
func (s *BlobGCService) StartCronScheduler(ctx context.Context, schedule string, minAge time.Duration) {
	if schedule == "" {
		s.log().Info("blob gc scheduler disabled: empty schedule")
		return
	}
	c := cron.New()
	_, err := c.AddFunc(schedule, func() {
		if _, err := s.CompactAll(context.Background(), GCOptions{MinAge: minAge}); err != nil {
			s.log().Error("blob gc cron error", "err", err)
		}
	})
	if err != nil {
		s.log().Error("blob gc: invalid schedule, scheduler disabled", "schedule", schedule, "err", err)
		return
	}
	c.Start()
	<-ctx.Done()
	c.Stop()
}
