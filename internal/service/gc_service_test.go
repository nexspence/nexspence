package service_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func buildGC(assetRepo *testutil.AssetRepo, bs *testutil.BlobStore) *service.BlobGCService {
	return &service.BlobGCService{Assets: assetRepo, BlobStore: bs}
}

func TestGC_NoBlobs(t *testing.T) {
	svc := buildGC(testutil.NewAssetRepo(), testutil.NewBlobStore())
	result, err := svc.Compact(context.Background(), false)
	require.NoError(t, err)
	assert.Equal(t, 0, result.ScannedBlobs)
	assert.Equal(t, 0, result.Orphans)
	assert.Equal(t, int64(0), result.FreedBytes)
}

func TestGC_AllReferenced(t *testing.T) {
	assets := testutil.NewAssetRepo()
	bs := testutil.NewBlobStore()
	ctx := context.Background()

	// Put a blob and a matching asset.
	require.NoError(t, bs.Put(ctx, "key1", bytes.NewReader([]byte("data")), 4))
	asset := &domain.Asset{
		ComponentID: "c1", RepositoryID: "r1", Repository: "repo",
		Path: "/file.txt", BlobKey: "key1", BlobStoreID: "bs1",
	}
	require.NoError(t, assets.Create(ctx, asset))

	svc := buildGC(assets, bs)
	result, err := svc.Compact(ctx, false)
	require.NoError(t, err)
	assert.Equal(t, 1, result.ScannedBlobs)
	assert.Equal(t, 0, result.Orphans)
	assert.True(t, bs.Has("key1"), "referenced blob must not be deleted")
}

func TestGC_OrphanDeleted(t *testing.T) {
	assets := testutil.NewAssetRepo()
	bs := testutil.NewBlobStore()
	ctx := context.Background()

	// Blob exists in store but has no asset.
	require.NoError(t, bs.Put(ctx, "orphan1", bytes.NewReader([]byte("garbage")), 7))

	svc := buildGC(assets, bs)
	result, err := svc.Compact(ctx, false)
	require.NoError(t, err)
	assert.Equal(t, 1, result.ScannedBlobs)
	assert.Equal(t, 1, result.Orphans)
	assert.Equal(t, int64(7), result.FreedBytes)
	assert.False(t, bs.Has("orphan1"), "orphaned blob must be deleted")
}

func TestGC_DryRunKeepsBlob(t *testing.T) {
	assets := testutil.NewAssetRepo()
	bs := testutil.NewBlobStore()
	ctx := context.Background()

	require.NoError(t, bs.Put(ctx, "orphan2", bytes.NewReader([]byte("dry")), 3))

	svc := buildGC(assets, bs)
	result, err := svc.Compact(ctx, true) // dry_run=true
	require.NoError(t, err)
	assert.Equal(t, 1, result.Orphans)
	assert.True(t, result.DryRun)
	assert.True(t, bs.Has("orphan2"), "dry run must not delete anything")
}

func TestGC_MixedReferencedAndOrphans(t *testing.T) {
	assets := testutil.NewAssetRepo()
	bs := testutil.NewBlobStore()
	ctx := context.Background()

	// Two blobs: one referenced, one orphaned.
	require.NoError(t, bs.Put(ctx, "good", bytes.NewReader([]byte("ok")), 2))
	require.NoError(t, bs.Put(ctx, "bad", bytes.NewReader([]byte("junk")), 4))

	asset := &domain.Asset{
		ComponentID: "c1", RepositoryID: "r1", Repository: "repo",
		Path: "/ok", BlobKey: "good", BlobStoreID: "bs1",
	}
	require.NoError(t, assets.Create(ctx, asset))

	svc := buildGC(assets, bs)
	result, err := svc.Compact(ctx, false)
	require.NoError(t, err)
	assert.Equal(t, 2, result.ScannedBlobs)
	assert.Equal(t, 1, result.Orphans)
	assert.Equal(t, int64(4), result.FreedBytes)
	assert.True(t, bs.Has("good"))
	assert.False(t, bs.Has("bad"))
}
