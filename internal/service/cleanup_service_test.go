package service_test

import (
	"context"
	"testing"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func nopLog() *zap.SugaredLogger { return zap.NewNop().Sugar() }

func TestRunAll_SkipsDisabledPolicies(t *testing.T) {
	policies := testutil.NewCleanupPolicyRepo(
		&domain.CleanupPolicy{
			ID: "p1", Name: "disabled-policy", Enabled: false, Format: "*",
			Criteria: map[string]any{"lastDownloadedDays": 30},
		},
	)
	assets := testutil.NewAssetRepo()
	blobs := testutil.NewBlobStore()

	svc := service.NewCleanupService(policies, assets, blobs, nopLog())
	require.NoError(t, svc.RunAll(context.Background()))

	// No update should have been recorded — policy was disabled
	assert.Empty(t, policies.Updates)
}

func TestRunAll_SkipsNoCriteriaPolicies(t *testing.T) {
	policies := testutil.NewCleanupPolicyRepo(
		&domain.CleanupPolicy{
			ID: "p2", Name: "empty-criteria", Enabled: true, Format: "*",
			Criteria: map[string]any{},
		},
	)
	assets := testutil.NewAssetRepo()
	blobs := testutil.NewBlobStore()

	svc := service.NewCleanupService(policies, assets, blobs, nopLog())
	require.NoError(t, svc.RunAll(context.Background()))

	// Policy has no useful criteria — nothing to run, no update expected
	assert.Empty(t, policies.Updates)
}

func TestRunAll_DeletesStaleAssets(t *testing.T) {
	staleAssets := []domain.Asset{
		{ID: "a1", BlobKey: "bk1", SizeBytes: 100, Path: "/foo.jar"},
		{ID: "a2", BlobKey: "bk2", SizeBytes: 200, Path: "/bar.jar"},
	}

	policies := testutil.NewCleanupPolicyRepo(
		&domain.CleanupPolicy{
			ID: "p3", Name: "stale-policy", Enabled: true, Format: "maven2",
			Criteria: map[string]any{"lastDownloadedDays": float64(30)},
		},
	)
	assets := testutil.NewAssetRepo()
	assets.Stale = staleAssets

	blobs := testutil.NewBlobStore()
	// Pre-populate blobs so Delete can "succeed"
	_ = blobs.Put(context.Background(), "bk1", testutil.MakeReader("data1"), 5)
	_ = blobs.Put(context.Background(), "bk2", testutil.MakeReader("data2"), 5)

	svc := service.NewCleanupService(policies, assets, blobs, nopLog())
	require.NoError(t, svc.RunAll(context.Background()))

	// Both blobs should be deleted
	assert.Contains(t, blobs.Deleted, "bk1")
	assert.Contains(t, blobs.Deleted, "bk2")

	// Policy stats should be updated
	require.Len(t, policies.Updates, 1)
	upd := policies.Updates[0]
	assert.Equal(t, 2, upd.LastRunCount)
	assert.Equal(t, int64(300), upd.LastRunFreed)
	assert.NotNil(t, upd.LastRunAt)
}

func TestRunAll_DryRun_DoesNotDelete(t *testing.T) {
	staleAssets := []domain.Asset{
		{ID: "a3", BlobKey: "bk3", SizeBytes: 50, Path: "/dry.tgz"},
	}

	policies := testutil.NewCleanupPolicyRepo(
		&domain.CleanupPolicy{
			ID: "p4", Name: "dry-run-policy", Enabled: true, Format: "*", DryRun: true,
			Criteria: map[string]any{"artifactAgeDays": float64(7)},
		},
	)
	assets := testutil.NewAssetRepo()
	assets.Stale = staleAssets
	blobs := testutil.NewBlobStore()

	svc := service.NewCleanupService(policies, assets, blobs, nopLog())
	require.NoError(t, svc.RunAll(context.Background()))

	// Dry-run: blob must NOT be deleted
	assert.Empty(t, blobs.Deleted)

	// But stats should still be recorded
	require.Len(t, policies.Updates, 1)
	upd := policies.Updates[0]
	assert.Equal(t, 1, upd.LastRunCount)
	assert.Equal(t, int64(50), upd.LastRunFreed)
}

func TestRunPolicy_NotFound(t *testing.T) {
	policies := testutil.NewCleanupPolicyRepo()
	assets := testutil.NewAssetRepo()
	blobs := testutil.NewBlobStore()

	svc := service.NewCleanupService(policies, assets, blobs, nopLog())
	err := svc.RunPolicy(context.Background(), "nonexistent-id")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestRunPolicy_ByID(t *testing.T) {
	staleAssets := []domain.Asset{
		{ID: "a5", BlobKey: "bk5", SizeBytes: 77, Path: "/specific.tgz"},
	}

	policies := testutil.NewCleanupPolicyRepo(
		&domain.CleanupPolicy{
			ID: "p5", Name: "specific", Enabled: true, Format: "*",
			Criteria: map[string]any{"lastDownloadedDays": float64(60)},
		},
	)
	assets := testutil.NewAssetRepo()
	assets.Stale = staleAssets
	blobs := testutil.NewBlobStore()
	_ = blobs.Put(context.Background(), "bk5", testutil.MakeReader("x"), 1)

	svc := service.NewCleanupService(policies, assets, blobs, nopLog())
	require.NoError(t, svc.RunPolicy(context.Background(), "p5"))

	assert.Contains(t, blobs.Deleted, "bk5")
}
