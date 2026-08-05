//go:build integration

package postgres

import (
	"context"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/testutil/pgtest"
)

// usedBytesOf reads a store's counter straight back from the row.
func usedBytesOf(t *testing.T, ctx context.Context, repo *blobStoreRepo, name string) int64 {
	t.Helper()
	bs, err := repo.Get(ctx, name)
	if err != nil {
		t.Fatalf("Get %q: %v", name, err)
	}
	return bs.UsedBytes
}

// Existing deployments carry counters inflated by the per-asset increment: an
// OCI manifest push registered two assets on one object and added its size
// twice. The repair has to count what is stored — one size per blob key —
// not what the asset rows add up to (issue #146).
func TestBlobStoreRepo_RecomputeUsedBytes_SharedBlobKeyCountedOnce(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	p := makeAssetParent(t, ctx, "recompute_shared")
	assets := NewAssetRepo(pool)
	repo := NewBlobStoreRepo(pool)

	const shared = "blobkey_shared_manifest"
	tag := makeAsset(p, "/manifests/app/1.0")
	tag.BlobKey = shared
	tag.SizeBytes = 500
	if err := assets.Create(ctx, tag); err != nil {
		t.Fatalf("Create tag asset: %v", err)
	}
	alias := makeAsset(p, "/manifests/app/sha256:abc")
	alias.BlobKey = shared
	alias.SizeBytes = 500
	if err := assets.Create(ctx, alias); err != nil {
		t.Fatalf("Create alias asset: %v", err)
	}

	storeName := "asset_bs_recompute_shared"
	// What the old code left behind: one size per asset.
	if err := repo.UpdateUsedBytes(ctx, storeName, 1000); err != nil {
		t.Fatalf("UpdateUsedBytes: %v", err)
	}

	if err := repo.RecomputeUsedBytes(ctx); err != nil {
		t.Fatalf("RecomputeUsedBytes: %v", err)
	}
	if got := usedBytesOf(t, ctx, repo, storeName); got != 500 {
		t.Errorf("used_bytes after recompute: got %d, want 500 (one object, one size)", got)
	}
}

// Each store is repaired from its own assets: bytes in one store never count
// against another.
func TestBlobStoreRepo_RecomputeUsedBytes_AttributesPerStore(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	a := makeAssetParent(t, ctx, "recompute_a")
	b := makeAssetParent(t, ctx, "recompute_b")
	assets := NewAssetRepo(pool)
	repo := NewBlobStoreRepo(pool)

	first := makeAsset(a, "/a/one.bin")
	first.SizeBytes = 300
	if err := assets.Create(ctx, first); err != nil {
		t.Fatalf("Create a: %v", err)
	}
	second := makeAsset(b, "/b/one.bin")
	second.SizeBytes = 70
	third := makeAsset(b, "/b/two.bin")
	third.SizeBytes = 30
	for _, as := range []*domain.Asset{second, third} {
		if err := assets.Create(ctx, as); err != nil {
			t.Fatalf("Create b: %v", err)
		}
	}

	if err := repo.UpdateUsedBytes(ctx, "asset_bs_recompute_a", 999999); err != nil {
		t.Fatalf("UpdateUsedBytes: %v", err)
	}

	if err := repo.RecomputeUsedBytes(ctx); err != nil {
		t.Fatalf("RecomputeUsedBytes: %v", err)
	}
	if got := usedBytesOf(t, ctx, repo, "asset_bs_recompute_a"); got != 300 {
		t.Errorf("store a: got %d, want 300", got)
	}
	if got := usedBytesOf(t, ctx, repo, "asset_bs_recompute_b"); got != 100 {
		t.Errorf("store b: got %d, want 100", got)
	}
}

// A store nothing points at holds nothing, however large its counter grew.
func TestBlobStoreRepo_RecomputeUsedBytes_StoreWithoutAssetsGoesToZero(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	repo := NewBlobStoreRepo(pool)
	bs := makeLocalBS("recompute_empty_bs")
	insertBS(t, ctx, repo, bs)
	if err := repo.UpdateUsedBytes(ctx, bs.Name, 4096); err != nil {
		t.Fatalf("UpdateUsedBytes: %v", err)
	}

	if err := repo.RecomputeUsedBytes(ctx); err != nil {
		t.Fatalf("RecomputeUsedBytes: %v", err)
	}
	if got := usedBytesOf(t, ctx, repo, bs.Name); got != 0 {
		t.Errorf("used_bytes: got %d, want 0", got)
	}
}

// assets.blob_store_id is NOT NULL today, and every write path resolves a store
// before inserting. The Go side nevertheless reads an unset store as "default"
// (DecrementBlobStoreUsage does), so the repair follows the same rule rather
// than dropping those bytes on the floor if the column is ever relaxed.
func TestBlobStoreRepo_RecomputeUsedBytes_UnsetStoreCountsAgainstDefault(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.Truncate(t, pool, "blob_stores", "repositories", "components")
	ctx := context.Background()

	p := makeAssetParent(t, ctx, "recompute_null")
	assets := NewAssetRepo(pool)
	repo := NewBlobStoreRepo(pool)

	def := makeLocalBS("default")
	insertBS(t, ctx, repo, def)

	if _, err := pool.Exec(ctx, `ALTER TABLE assets ALTER COLUMN blob_store_id DROP NOT NULL`); err != nil {
		t.Fatalf("relax blob_store_id: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM assets WHERE blob_store_id IS NULL`)
		if _, err := pool.Exec(context.Background(),
			`ALTER TABLE assets ALTER COLUMN blob_store_id SET NOT NULL`); err != nil {
			t.Fatalf("restore blob_store_id NOT NULL: %v", err)
		}
	})

	orphanRow := makeAsset(p, "/no-store/file.bin")
	orphanRow.SizeBytes = 42
	if err := assets.Create(ctx, orphanRow); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE assets SET blob_store_id = NULL WHERE id = $1`, orphanRow.ID); err != nil {
		t.Fatalf("null out blob_store_id: %v", err)
	}

	if err := repo.RecomputeUsedBytes(ctx); err != nil {
		t.Fatalf("RecomputeUsedBytes: %v", err)
	}
	if got := usedBytesOf(t, ctx, repo, "default"); got != 42 {
		t.Errorf("default store: got %d, want 42", got)
	}
}
