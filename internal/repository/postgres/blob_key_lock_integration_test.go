//go:build integration

package postgres

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nexspence-oss/nexspence/internal/testutil/pgtest"
)

func smallLockPool(t *testing.T, maxConns int32) *pgxpool.Pool {
	t.Helper()
	cfg := pgtest.Pool(t).Config().Copy()
	cfg.MaxConns = maxConns
	cfg.MinConns = 0
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestAssetRepo_WithBlobKeyLock_NestedSingleConnection(t *testing.T) {
	pool := smallLockPool(t, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	repo := NewAssetRepo(pool)
	err := repo.WithBlobKeyLock(ctx, "nested-manifest", func(ctx context.Context) error {
		return repo.WithBlobKeyLock(ctx, "nested-digest", func(ctx context.Context) error {
			return repo.WithBlobKeyLock(ctx, "quota:store:test-nested", func(ctx context.Context) error {
				return repo.WithBlobKeyLock(ctx, "quota:repo:test-nested", func(ctx context.Context) error {
					// A reentrant acquisition is balanced without releasing the outer lock.
					return repo.WithBlobKeyLock(ctx, "nested-digest", func(ctx context.Context) error {
						_, err := repo.CountByBlobKey(ctx, "nested-digest", "")
						return err
					})
				})
			})
		})
	})
	if err != nil {
		t.Fatalf("nested lock/query with MaxConns=1: %v", err)
	}
}

func TestAssetRepo_WithBlobKeyLock_WaitersLeaveConnectionForOtherKeys(t *testing.T) {
	pool := smallLockPool(t, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	repo := NewAssetRepo(pool)
	holder := make(chan struct{})
	release := make(chan struct{})
	results := make(chan error, 9)
	go func() {
		results <- repo.WithBlobKeyLock(ctx, "waiter-manifest", func(ctx context.Context) error {
			close(holder)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
			_, err := repo.CountByBlobKey(ctx, "waiter-manifest", "")
			return err
		})
	}()
	<-holder
	var wg sync.WaitGroup
	before := pool.Stat().AcquireCount()
	for range 8 {
		wg.Go(func() {
			results <- repo.WithBlobKeyLock(ctx, "waiter-manifest", func(ctx context.Context) error { return nil })
		})
	}
	// Observe actual acquisition attempts instead of relying on a scheduler sleep.
	for pool.Stat().AcquireCount() < before+8 && ctx.Err() == nil {
		time.Sleep(time.Millisecond)
	}
	err := repo.WithBlobKeyLock(ctx, "other-manifest", func(ctx context.Context) error {
		_, err := repo.CountByBlobKey(ctx, "other-manifest", "")
		return err
	})
	close(release)
	wg.Wait()
	if err != nil {
		t.Errorf("other key starved by waiters: %v", err)
	}
	for range 9 {
		if err := <-results; err != nil {
			t.Errorf("lock: %v", err)
		}
	}
}

func TestAssetRepo_WithBlobKeyLock_ReleasesAfterCallbackExit(t *testing.T) {
	for _, kind := range []string{"error", "cancel", "panic"} {
		t.Run(kind, func(t *testing.T) {
			pool := smallLockPool(t, 1)
			repo := NewAssetRepo(pool)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			func() {
				defer func() {
					if got := recover(); got != nil && kind != "panic" {
						t.Errorf("unexpected panic: %v", got)
					}
				}()
				err := repo.WithBlobKeyLock(ctx, "exit-manifest", func(context.Context) error {
					if kind == "panic" {
						panic("test lock cleanup")
					}
					if kind == "cancel" {
						cancel()
					}
					return context.Canceled
				})
				if !errors.Is(err, context.Canceled) {
					t.Errorf("callback error = %v", err)
				}
			}()
			// Another session must acquire it, even if the original session remains idle
			// in its pool. Reacquiring on that original session would hide a leaked lock.
			other := smallLockPool(t, 1)
			checkCtx, stop := context.WithTimeout(context.Background(), time.Second)
			defer stop()
			if err := NewAssetRepo(other).WithBlobKeyLock(checkCtx, "exit-manifest", func(context.Context) error { return nil }); err != nil {
				t.Fatalf("lock leaked after %s: %v", kind, err)
			}
		})
	}
}

func TestAssetRepo_WithBlobKeyLock_ErrorPreservesCallbackWrites(t *testing.T) {
	pgtest.Truncate(t, pgtest.Pool(t), "blob_stores", "repositories", "components")
	parent := makeAssetParent(t, context.Background(), "lock_autocommit")
	pool := smallLockPool(t, 1)
	repo := NewAssetRepo(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	asset := makeAsset(parent, "/lock-autocommit")
	err := repo.WithBlobKeyLock(ctx, asset.BlobKey, func(ctx context.Context) error {
		if err := repo.Create(ctx, asset); err != nil {
			return err
		}
		count, err := repo.CountByBlobKey(ctx, asset.BlobKey, "")
		if err != nil {
			return err
		}
		if count != 1 {
			t.Errorf("count without exclusion = %d, want 1", count)
		}
		count, err = repo.CountByBlobKey(ctx, asset.BlobKey, asset.ID)
		if err != nil {
			return err
		}
		if count != 0 {
			t.Errorf("count excluding asset = %d, want 0", count)
		}
		return context.Canceled
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("callback error: %v", err)
	}
	if _, err := repo.Get(ctx, asset.ID); err != nil {
		t.Fatalf("callback write was rolled back: %v", err)
	}
}

func TestAssetRepo_WithBlobKeyLock_CanceledNestedWaitDiscardsSession(t *testing.T) {
	holderPool := smallLockPool(t, 1)
	pool := smallLockPool(t, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- NewAssetRepo(holderPool).WithBlobKeyLock(ctx, "cancel-nested-inner", func(ctx context.Context) error {
			close(entered)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	<-entered
	repo := NewAssetRepo(pool)
	err := repo.WithBlobKeyLock(ctx, "cancel-nested-outer", func(ctx context.Context) error {
		nestedCtx, stop := context.WithTimeout(ctx, 30*time.Millisecond)
		defer stop()
		return repo.WithBlobKeyLock(nestedCtx, "cancel-nested-inner", func(context.Context) error {
			t.Error("canceled waiter entered callback")
			return nil
		})
	})
	close(release)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("nested wait error = %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// A fresh connection can take both keys, so no uncertain lock session escaped
	// back into the pool after cancellation during pg_advisory_lock.
	for _, key := range []string{"cancel-nested-inner", "cancel-nested-outer"} {
		if err := NewAssetRepo(holderPool).WithBlobKeyLock(ctx, key, func(context.Context) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
}
