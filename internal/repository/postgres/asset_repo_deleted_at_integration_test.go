//go:build integration

package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/nexspence-oss/nexspence/internal/testutil/pgtest"
)

// #728: every way an asset row goes away records when, on its repository
// only — Delete, DeleteIfUnchanged, and a cascade from its component.
func TestAssetRepo_LastDeletedAt_EveryDeletePath(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx := context.Background()
	repo := NewAssetRepo(pool)

	p := makeAssetParent(t, ctx, "deleted_at")
	other := makeAssetParent(t, ctx, "deleted_at_other")
	if err := repo.Create(ctx, makeAsset(other, "/keep.bin")); err != nil {
		t.Fatalf("create: %v", err)
	}
	at, err := repo.LastDeletedAt(ctx, p.RepoName)
	if err != nil || !at.IsZero() {
		t.Fatalf("nothing deleted yet: at=%v err=%v", at, err)
	}

	step := func(name string, del func(t *testing.T)) {
		t.Helper()
		before := time.Now().Add(-time.Second)
		del(t)
		at, err := repo.LastDeletedAt(ctx, p.RepoName)
		if err != nil || at.Before(before) {
			t.Errorf("%s: deletion not recorded: at=%v err=%v", name, at, err)
		}
	}
	a := makeAsset(p, "/a.bin")
	if err := repo.Create(ctx, a); err != nil {
		t.Fatalf("create: %v", err)
	}
	step("Delete", func(t *testing.T) {
		if err := repo.Delete(ctx, a.ID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
	})
	b := makeAsset(p, "/b.bin")
	if err := repo.Create(ctx, b); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, _ := repo.Get(ctx, b.ID)
	step("DeleteIfUnchanged", func(t *testing.T) {
		if ok, err := repo.DeleteIfUnchanged(ctx, b.ID, got.BlobKey, got.LastDownloaded, got.LastModified); err != nil || !ok {
			t.Fatalf("DeleteIfUnchanged: ok=%v err=%v", ok, err)
		}
	})
	if err := repo.Create(ctx, makeAsset(p, "/c.bin")); err != nil {
		t.Fatalf("create: %v", err)
	}
	step("component cascade", func(t *testing.T) {
		if err := NewComponentRepo(pool).Delete(ctx, p.ComponentID); err != nil {
			t.Fatalf("delete component: %v", err)
		}
	})

	if at, _ := repo.LastDeletedAt(ctx, other.RepoName); !at.IsZero() {
		t.Errorf("another repository's deletes must not touch it, got %v", at)
	}
	if at, err := repo.LastDeletedAt(ctx, "no-such-repo"); err != nil || !at.IsZero() {
		t.Errorf("unknown repository: at=%v err=%v", at, err)
	}
}

// A failed lookup is an error, not "nothing deleted": the caller's fallback
// must run rather than serve an older date.
func TestAssetRepo_LastDeletedAt_ErrorIsNotZero(t *testing.T) {
	pool := pgtest.Pool(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewAssetRepo(pool).LastDeletedAt(ctx, "any"); err == nil {
		t.Fatal("a cancelled lookup must return its error")
	}
}

// Deleting a repository locks its row, then its assets (cascade); an asset
// delete locks asset rows, then runs the trigger. The trigger must never wait
// for the repository row, or the two deadlock.
func TestAssetRepo_DeletionTriggerDoesNotDeadlockWithRepositoryDelete(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	p := makeAssetParent(t, ctx, "deadlock")
	a := makeAsset(p, "/held.bin")
	if err := NewAssetRepo(pool).Create(ctx, a); err != nil {
		t.Fatalf("create: %v", err)
	}

	assetTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = assetTx.Rollback(ctx) }()
	if _, err := assetTx.Exec(ctx, `SELECT 1 FROM assets WHERE id = $1 FOR UPDATE`, a.ID); err != nil {
		t.Fatalf("lock asset row: %v", err)
	}

	repoDeleted := make(chan error, 1)
	go func() {
		_, err := pool.Exec(ctx, `DELETE FROM repositories WHERE id = $1`, p.RepositoryID)
		repoDeleted <- err
	}()
	// Let the repository delete take its row and block on the asset row.
	for i := 0; i < 100; i++ {
		var waiting bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity
			WHERE wait_event_type = 'Lock' AND query LIKE 'DELETE FROM repositories%')`).Scan(&waiting); err == nil && waiting {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if _, err := assetTx.Exec(ctx, `DELETE FROM assets WHERE id = $1`, a.ID); err != nil {
		t.Fatalf("asset delete (trigger) must not deadlock: %v", err)
	}
	if err := assetTx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := <-repoDeleted; err != nil {
		t.Fatalf("repository delete must not deadlock: %v", err)
	}
}
