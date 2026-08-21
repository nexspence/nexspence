package service_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// defaultStoreID is the id testutil.NewBlobStoreRepo seeds the "default" store
// with. GC now asks "referenced in THIS store?", so a fixture asset has to sit
// on the store under compaction to count as a reference.
const defaultStoreID = "00000000-0000-0000-0000-000000000001"

func buildGC(assets *testutil.AssetRepo, bs *testutil.BlobStore) *service.BlobGCService {
	return &service.BlobGCService{
		Assets:   assets,
		Stores:   testutil.NewBlobStoreRepo(), // provides a "default" store
		Resolver: testutil.NewFakeResolver(bs),
	}
}

func TestGC_NoBlobs(t *testing.T) {
	svc := buildGC(testutil.NewAssetRepo(), testutil.NewBlobStore())
	result, err := svc.CompactStore(context.Background(), "default", service.GCOptions{})
	require.NoError(t, err)
	assert.Equal(t, 0, result.ScannedBlobs)
	assert.Equal(t, 0, result.Orphans)
	assert.Equal(t, int64(0), result.FreedBytes)
	assert.Equal(t, "default", result.Store)
}

func TestGC_AllReferenced(t *testing.T) {
	assets := testutil.NewAssetRepo()
	bs := testutil.NewBlobStore()
	ctx := context.Background()

	require.NoError(t, bs.Put(ctx, "key1", bytes.NewReader([]byte("data")), 4))
	require.NoError(t, assets.Create(ctx, &domain.Asset{
		ComponentID: "c1", RepositoryID: "r1", Repository: "repo",
		Path: "/file.txt", BlobKey: "key1", BlobStoreID: defaultStoreID,
	}))

	svc := buildGC(assets, bs)
	result, err := svc.CompactStore(ctx, "default", service.GCOptions{})
	require.NoError(t, err)
	assert.Equal(t, 1, result.ScannedBlobs)
	assert.Equal(t, 0, result.Orphans)
	assert.True(t, bs.Has("key1"))
}

func TestGC_OrphanDeleted(t *testing.T) {
	assets := testutil.NewAssetRepo()
	bs := testutil.NewBlobStore()
	ctx := context.Background()
	require.NoError(t, bs.Put(ctx, "orphan1", bytes.NewReader([]byte("garbage")), 7))

	svc := buildGC(assets, bs)
	result, err := svc.CompactStore(ctx, "default", service.GCOptions{})
	require.NoError(t, err)
	assert.Equal(t, 1, result.Orphans)
	assert.Equal(t, int64(7), result.FreedBytes)
	assert.False(t, bs.Has("orphan1"))
}

func TestGC_DryRunKeepsBlob(t *testing.T) {
	assets := testutil.NewAssetRepo()
	bs := testutil.NewBlobStore()
	ctx := context.Background()
	require.NoError(t, bs.Put(ctx, "orphan2", bytes.NewReader([]byte("dry")), 3))

	svc := buildGC(assets, bs)
	result, err := svc.CompactStore(ctx, "default", service.GCOptions{DryRun: true})
	require.NoError(t, err)
	assert.Equal(t, 1, result.Orphans)
	assert.True(t, result.DryRun)
	assert.True(t, bs.Has("orphan2"))
}

func TestGC_FreshOrphanRetainedByMinAge(t *testing.T) {
	assets := testutil.NewAssetRepo()
	bs := testutil.NewBlobStore()
	ctx := context.Background()
	require.NoError(t, bs.Put(ctx, "fresh", bytes.NewReader([]byte("new")), 3))
	// Put sets mtime = now, so a 24h grace period must retain it.

	svc := buildGC(assets, bs)
	result, err := svc.CompactStore(ctx, "default", service.GCOptions{MinAge: 24 * time.Hour})
	require.NoError(t, err)
	assert.Equal(t, 0, result.Orphans, "fresh orphan must be retained")
	assert.True(t, bs.Has("fresh"))
}

func TestGC_OldOrphanCollectedByMinAge(t *testing.T) {
	assets := testutil.NewAssetRepo()
	bs := testutil.NewBlobStore()
	ctx := context.Background()
	require.NoError(t, bs.Put(ctx, "old", bytes.NewReader([]byte("old")), 3))
	bs.SetMTime("old", time.Now().Add(-48*time.Hour))

	svc := buildGC(assets, bs)
	result, err := svc.CompactStore(ctx, "default", service.GCOptions{MinAge: 24 * time.Hour})
	require.NoError(t, err)
	assert.Equal(t, 1, result.Orphans)
	assert.False(t, bs.Has("old"))
}

func TestGC_CompactAllIteratesStores(t *testing.T) {
	assets := testutil.NewAssetRepo()
	bs := testutil.NewBlobStore()
	ctx := context.Background()
	require.NoError(t, bs.Put(ctx, "junk", bytes.NewReader([]byte("x")), 1))

	svc := buildGC(assets, bs) // testutil repo yields exactly one store: "default"
	results, err := svc.CompactAll(ctx, service.GCOptions{})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, 1, results[0].Orphans)
	assert.False(t, bs.Has("junk"))
}

func TestGC_CompactStoreUnknownName(t *testing.T) {
	svc := buildGC(testutil.NewAssetRepo(), testutil.NewBlobStore())
	_, err := svc.CompactStore(context.Background(), "does-not-exist", service.GCOptions{})
	require.Error(t, err)
}

func TestGC_StartCronScheduler_EmptyScheduleReturns(t *testing.T) {
	svc := buildGC(testutil.NewAssetRepo(), testutil.NewBlobStore())
	// Empty schedule must return immediately (no goroutine left blocking).
	svc.StartCronScheduler(context.Background(), "", 0)
}

func TestGC_StartCronScheduler_InvalidScheduleReturns(t *testing.T) {
	svc := buildGC(testutil.NewAssetRepo(), testutil.NewBlobStore())
	svc.StartCronScheduler(context.Background(), "not-a-valid-cron", 0)
}

func TestGC_StartCronScheduler_ValidScheduleStopsOnCancel(t *testing.T) {
	svc := buildGC(testutil.NewAssetRepo(), testutil.NewBlobStore())
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel so StartCronScheduler starts then returns at <-ctx.Done()
	svc.StartCronScheduler(ctx, "0 0 * * *", time.Hour)
}

// ── Usage accounting ──────────────────────────────────────────

// gcWithStores builds the service over a blob store repo the test can read back,
// so the effect of a collection on used_bytes is visible.
func gcWithStores(assets *testutil.AssetRepo, bs *testutil.BlobStore,
	stores *testutil.BlobStoreRepo) *service.BlobGCService {
	return &service.BlobGCService{
		Assets:   assets,
		Stores:   stores,
		Resolver: testutil.NewFakeResolver(bs),
	}
}

// Collecting an orphan takes bytes off the disk, so it has to take them off the
// counter that says how full the store is — otherwise a garbage-collected store
// stays permanently overstated and walks into a quota rejection (#146).
func TestGC_OrphanCollected_DecrementsTheStoreUsage(t *testing.T) {
	assets := testutil.NewAssetRepo()
	bs := testutil.NewBlobStore()
	ctx := context.Background()
	require.NoError(t, bs.Put(ctx, "orphan-usage", bytes.NewReader([]byte("garbage")), 7))

	stores := testutil.NewBlobStoreRepo()
	require.NoError(t, stores.UpdateUsedBytes(ctx, "default", 30)) // 7 orphaned, 23 still referenced

	result, err := gcWithStores(assets, bs, stores).CompactStore(ctx, "default", service.GCOptions{})
	require.NoError(t, err)
	require.Equal(t, int64(7), result.FreedBytes)

	got, err := stores.Get(ctx, "default")
	require.NoError(t, err)
	assert.Equal(t, int64(23), got.UsedBytes, "the freed bytes come off the store")
}

// A dry run reports what it would free and frees nothing, so the counter must
// not move either.
func TestGC_DryRun_LeavesTheStoreUsageAlone(t *testing.T) {
	assets := testutil.NewAssetRepo()
	bs := testutil.NewBlobStore()
	ctx := context.Background()
	require.NoError(t, bs.Put(ctx, "orphan-dry", bytes.NewReader([]byte("dry")), 3))

	stores := testutil.NewBlobStoreRepo()
	require.NoError(t, stores.UpdateUsedBytes(ctx, "default", 10))

	_, err := gcWithStores(assets, bs, stores).CompactStore(ctx, "default", service.GCOptions{DryRun: true})
	require.NoError(t, err)

	got, err := stores.Get(ctx, "default")
	require.NoError(t, err)
	assert.Equal(t, int64(10), got.UsedBytes)
}

// A blob that is still referenced is neither deleted nor discounted.
func TestGC_ReferencedBlob_LeavesTheStoreUsageAlone(t *testing.T) {
	assets := testutil.NewAssetRepo()
	bs := testutil.NewBlobStore()
	ctx := context.Background()
	require.NoError(t, bs.Put(ctx, "kept", bytes.NewReader([]byte("data")), 4))
	require.NoError(t, assets.Create(ctx, &domain.Asset{
		ComponentID: "c1", RepositoryID: "r1", Repository: "repo",
		Path: "/file.txt", BlobKey: "kept", BlobStoreID: defaultStoreID, SizeBytes: 4,
	}))

	stores := testutil.NewBlobStoreRepo()
	require.NoError(t, stores.UpdateUsedBytes(ctx, "default", 4))

	_, err := gcWithStores(assets, bs, stores).CompactStore(ctx, "default", service.GCOptions{})
	require.NoError(t, err)

	got, err := stores.Get(ctx, "default")
	require.NoError(t, err)
	assert.Equal(t, int64(4), got.UsedBytes)
}

func TestGC_CompactAllSkipsWhenLockHeld(t *testing.T) {
	assets := testutil.NewAssetRepo()
	bs := testutil.NewBlobStore()
	ctx := context.Background()
	require.NoError(t, bs.Put(ctx, "junk", bytes.NewReader([]byte("x")), 1))

	svc := buildGC(assets, bs)
	svc.Locker = testutil.NewHeldLocker() // Acquire always returns distlock.ErrLockHeld

	results, err := svc.CompactAll(ctx, service.GCOptions{})
	require.NoError(t, err)
	assert.Nil(t, results, "must skip when another node holds the lock")
	assert.True(t, bs.Has("junk"), "nothing collected when skipped")
}

// A copy left behind in a store no asset points at any more is an orphan, even
// though the very same key is alive in another store — what a blob-store
// migration leaves behind, and what the old key-only reference set protected
// forever (#297).
func TestGC_KeyReferencedInAnotherStore_IsCollectedHere(t *testing.T) {
	assets := testutil.NewAssetRepo()
	source := testutil.NewBlobStore()
	ctx := context.Background()

	require.NoError(t, source.Put(ctx, "key1", bytes.NewReader([]byte("data")), 4))
	// The asset was migrated: same key, now living on another store.
	require.NoError(t, assets.Create(ctx, &domain.Asset{
		ComponentID: "c1", RepositoryID: "r1", Repository: "repo",
		Path: "/file.txt", BlobKey: "key1", BlobStoreID: "store-target", SizeBytes: 4,
	}))

	result, err := buildGC(assets, source).CompactStore(ctx, "default", service.GCOptions{})
	require.NoError(t, err)
	assert.Equal(t, 1, result.Orphans)
	assert.False(t, source.Has("key1"))
}

// An asset row with no store id names a key but not a location. Collecting on
// that would delete live blobs, so it counts as referenced in every store.
func TestGC_AssetWithoutStoreID_ProtectsEveryStore(t *testing.T) {
	assets := testutil.NewAssetRepo()
	bs := testutil.NewBlobStore()
	ctx := context.Background()

	require.NoError(t, bs.Put(ctx, "key1", bytes.NewReader([]byte("data")), 4))
	require.NoError(t, assets.Create(ctx, &domain.Asset{
		ComponentID: "c1", RepositoryID: "r1", Repository: "repo",
		Path: "/file.txt", BlobKey: "key1",
	}))

	result, err := buildGC(assets, bs).CompactStore(ctx, "default", service.GCOptions{})
	require.NoError(t, err)
	assert.Equal(t, 0, result.Orphans)
	assert.True(t, bs.Has("key1"))
}
