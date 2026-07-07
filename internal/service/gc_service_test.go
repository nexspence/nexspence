package service_test

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func buildGC(assets *testutil.AssetRepo, bs *testutil.BlobStore) *service.BlobGCService {
	return &service.BlobGCService{
		Assets:   assets,
		Stores:   testutil.NewBlobStoreRepo(), // provides a "default" store
		Resolver: testutil.NewFakeResolver(bs),
		Log:      slog.Default(),
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
		Path: "/file.txt", BlobKey: "key1", BlobStoreID: "bs1",
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
