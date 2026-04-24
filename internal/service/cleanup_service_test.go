package service_test

import (
	"context"
	"testing"
	"time"

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
	blobRepo := testutil.NewBlobStoreRepo()
	repos := testutil.NewRepoRepo()

	svc := service.NewCleanupService(policies, repos, assets, blobRepo, blobs, nopLog())
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
	blobRepo := testutil.NewBlobStoreRepo()
	repos := testutil.NewRepoRepo()

	svc := service.NewCleanupService(policies, repos, assets, blobRepo, blobs, nopLog())
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
	blobRepo := testutil.NewBlobStoreRepo()
	// Pre-populate blobs so Delete can "succeed"
	_ = blobs.Put(context.Background(), "bk1", testutil.MakeReader("data1"), 5)
	_ = blobs.Put(context.Background(), "bk2", testutil.MakeReader("data2"), 5)

	repos := testutil.NewRepoRepo(&domain.Repository{
		Name: "maven-hosted", ID: "r1", Format: domain.FormatMaven2,
		CleanupPolicyIDs: []string{"p3"},
	})

	svc := service.NewCleanupService(policies, repos, assets, blobRepo, blobs, nopLog())
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
	blobRepo := testutil.NewBlobStoreRepo()
	repos := testutil.NewRepoRepo(&domain.Repository{
		Name: "hosted", ID: "r1", Format: domain.FormatRaw,
		CleanupPolicyIDs: []string{"p4"},
	})

	svc := service.NewCleanupService(policies, repos, assets, blobRepo, blobs, nopLog())
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
	blobRepo := testutil.NewBlobStoreRepo()
	repos := testutil.NewRepoRepo()

	svc := service.NewCleanupService(policies, repos, assets, blobRepo, blobs, nopLog())
	err := svc.RunPolicy(context.Background(), "nonexistent-id")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestRunPolicy_SkipsWhenNoRepositoriesAttached(t *testing.T) {
	policies := testutil.NewCleanupPolicyRepo(
		&domain.CleanupPolicy{
			ID: "p6", Name: "unattached", Enabled: true, Format: "*",
			Criteria: map[string]any{"lastDownloadedDays": float64(1)},
		},
	)
	assets := testutil.NewAssetRepo()
	assets.Stale = []domain.Asset{{ID: "x", BlobKey: "k", SizeBytes: 1, Path: "/p"}}
	blobs := testutil.NewBlobStore()
	blobRepo := testutil.NewBlobStoreRepo()
	repos := testutil.NewRepoRepo()

	svc := service.NewCleanupService(policies, repos, assets, blobRepo, blobs, nopLog())
	require.NoError(t, svc.RunPolicy(context.Background(), "p6"))

	assert.Empty(t, blobs.Deleted)
	assert.Empty(t, policies.Updates)
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
	blobRepo := testutil.NewBlobStoreRepo()
	_ = blobs.Put(context.Background(), "bk5", testutil.MakeReader("x"), 1)

	repos := testutil.NewRepoRepo(&domain.Repository{
		Name: "hosted", ID: "r1", Format: domain.FormatRaw,
		CleanupPolicyIDs: []string{"p5"},
	})

	svc := service.NewCleanupService(policies, repos, assets, blobRepo, blobs, nopLog())
	require.NoError(t, svc.RunPolicy(context.Background(), "p5"))

	assert.Contains(t, blobs.Deleted, "bk5")
}

func TestReloadPolicy_NoopWhenSchedulerNotStarted(t *testing.T) {
	policies := testutil.NewCleanupPolicyRepo(
		&domain.CleanupPolicy{
			ID: "p10", Name: "policy", Enabled: true, Format: "*",
			ScheduleCron: "* * * * *",
			Criteria:     map[string]any{"artifactAgeDays": float64(1)},
		},
	)
	svc := service.NewCleanupService(policies, testutil.NewRepoRepo(), testutil.NewAssetRepo(), testutil.NewBlobStoreRepo(), testutil.NewBlobStore(), nopLog())

	// Must not panic before StartCronScheduler is called
	svc.ReloadPolicy(context.Background(), "p10")
	svc.ReloadPolicy(context.Background(), "nonexistent")
}

func TestReloadPolicy_RemovesEntryForDeletedPolicy(t *testing.T) {
	p := &domain.CleanupPolicy{
		ID: "p11", Name: "removable", Enabled: true, Format: "*",
		ScheduleCron: "@yearly", // won't fire during test
		Criteria:     map[string]any{"artifactAgeDays": float64(365)},
	}
	policies := testutil.NewCleanupPolicyRepo(p)
	svc := service.NewCleanupService(policies, testutil.NewRepoRepo(), testutil.NewAssetRepo(), testutil.NewBlobStoreRepo(), testutil.NewBlobStore(), nopLog())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.StartCronScheduler(ctx, "@yearly")
	time.Sleep(50 * time.Millisecond) // wait for cron to start

	// Simulate deletion: remove from mock repo, then reload
	policies.Delete(context.Background(), "p11")
	// Should not panic — entry removed, policy not found
	svc.ReloadPolicy(context.Background(), "p11")
}

func TestStartCronScheduler_InvalidCronFallsBackToDefault(t *testing.T) {
	p := &domain.CleanupPolicy{
		ID: "p12", Name: "bad-cron", Enabled: true, Format: "*",
		ScheduleCron: "NOT_A_VALID_CRON",
		Criteria:     map[string]any{"artifactAgeDays": float64(1)},
	}
	policies := testutil.NewCleanupPolicyRepo(p)
	svc := service.NewCleanupService(policies, testutil.NewRepoRepo(), testutil.NewAssetRepo(), testutil.NewBlobStoreRepo(), testutil.NewBlobStore(), nopLog())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Must not panic — falls back to default schedule
	go svc.StartCronScheduler(ctx, "@yearly")
	time.Sleep(50 * time.Millisecond)
}
