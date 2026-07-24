package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
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

	// Policy run stats should be recorded
	require.Len(t, policies.RunRecords, 1)
	rec := policies.RunRecords[0]
	assert.Equal(t, 2, rec.Count)
	assert.Equal(t, int64(300), rec.Freed)
	assert.False(t, rec.At.IsZero())
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
	require.Len(t, policies.RunRecords, 1)
	rec := policies.RunRecords[0]
	assert.Equal(t, 1, rec.Count)
	assert.Equal(t, int64(50), rec.Freed)
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

func TestRunPolicy_RetainNVersions_PassedToListStale(t *testing.T) {
	staleAssets := []domain.Asset{
		{ID: "a10", BlobKey: "bk10", SizeBytes: 10, Path: "/old.jar"},
	}
	policies := testutil.NewCleanupPolicyRepo(
		&domain.CleanupPolicy{
			ID: "p20", Name: "retain-test", Enabled: true, Format: "*",
			Criteria:        map[string]any{"artifactAgeDays": float64(30)},
			RetainNVersions: 3,
		},
	)
	assets := testutil.NewAssetRepo()
	assets.Stale = staleAssets
	blobs := testutil.NewBlobStore()
	blobRepo := testutil.NewBlobStoreRepo()
	_ = blobs.Put(context.Background(), "bk10", testutil.MakeReader("x"), 1)
	repos := testutil.NewRepoRepo(&domain.Repository{
		Name: "hosted", ID: "r1", Format: domain.FormatRaw,
		CleanupPolicyIDs: []string{"p20"},
	})

	svc := service.NewCleanupService(policies, repos, assets, blobRepo, blobs, nopLog())
	require.NoError(t, svc.RunPolicy(context.Background(), "p20"))

	assert.Equal(t, 3, assets.LastRetainN)
	assert.Contains(t, blobs.Deleted, "bk10")
}

// ── #62: clear-all, run reporting, per-store blob delete ──────────

// A policy with no age/download criteria but attached to a repository means
// "delete everything in that repo" (issue #62: "completely clear every 2 min").
// Previously the service skipped any policy with both age criteria at zero, so a
// clear-all policy did nothing and never recorded a run.
func TestRunPolicy_NoAgeCriteria_AttachedRepo_DeletesAll(t *testing.T) {
	staleAssets := []domain.Asset{
		{ID: "a1", BlobKey: "bk1", SizeBytes: 100, Path: "/pool/a.deb"},
		{ID: "a2", BlobKey: "bk2", SizeBytes: 200, Path: "/pool/b.deb"},
	}
	policies := testutil.NewCleanupPolicyRepo(&domain.CleanupPolicy{
		ID: "clr", Name: "clear-all", Enabled: true, Format: "*",
		Criteria: map[string]any{}, // no age/download filter → delete everything
	})
	assets := testutil.NewAssetRepo()
	assets.Stale = staleAssets
	blobs := testutil.NewBlobStore()
	_ = blobs.Put(context.Background(), "bk1", testutil.MakeReader("x"), 1)
	_ = blobs.Put(context.Background(), "bk2", testutil.MakeReader("y"), 1)
	blobRepo := testutil.NewBlobStoreRepo()
	repos := testutil.NewRepoRepo(&domain.Repository{
		Name: "debian", ID: "r1", Format: domain.FormatApt,
		CleanupPolicyIDs: []string{"clr"},
	})

	svc := service.NewCleanupService(policies, repos, assets, blobRepo, blobs, nopLog())
	res, err := svc.RunPolicyResult(context.Background(), "clr")
	require.NoError(t, err)
	require.NotNil(t, res)

	assert.False(t, res.Skipped, "attached clear-all policy must run")
	assert.Equal(t, 2, res.Deleted)
	assert.Equal(t, int64(300), res.FreedBytes)
	assert.Contains(t, blobs.Deleted, "bk1")
	assert.Contains(t, blobs.Deleted, "bk2")

	require.Len(t, policies.RunRecords, 1)
	assert.False(t, policies.RunRecords[0].At.IsZero(), "a run must be recorded")
}

// The same clear-all policy expressed via Scope.RepositoryName (no attachment).
func TestRunPolicy_NoAgeCriteria_ScopedRepo_DeletesAll(t *testing.T) {
	policies := testutil.NewCleanupPolicyRepo(&domain.CleanupPolicy{
		ID: "clr", Name: "clear-scope", Enabled: true, Format: "apt",
		Criteria: map[string]any{},
		Scope:    domain.CleanupScope{RepositoryName: "debian"},
	})
	assets := testutil.NewAssetRepo()
	assets.Stale = []domain.Asset{{ID: "a1", BlobKey: "bk1", SizeBytes: 50, Path: "/x"}}
	blobs := testutil.NewBlobStore()
	_ = blobs.Put(context.Background(), "bk1", testutil.MakeReader("x"), 1)
	blobRepo := testutil.NewBlobStoreRepo()
	repos := testutil.NewRepoRepo(&domain.Repository{Name: "debian", ID: "r1", Format: domain.FormatApt})

	svc := service.NewCleanupService(policies, repos, assets, blobRepo, blobs, nopLog())
	res, err := svc.RunPolicyResult(context.Background(), "clr")
	require.NoError(t, err)
	assert.False(t, res.Skipped)
	assert.Equal(t, 1, res.Deleted)
	assert.Contains(t, blobs.Deleted, "bk1")
}

// A policy attached to nothing (no scope, no repos) reports a skip with a reason
// so the manual-run endpoint can tell the user why nothing happened — instead of
// the old silent 202.
func TestRunPolicyResult_NotAttached_ReportsSkip(t *testing.T) {
	policies := testutil.NewCleanupPolicyRepo(&domain.CleanupPolicy{
		ID: "orphan", Name: "orphan", Enabled: true, Format: "*",
		Criteria: map[string]any{"artifactAgeDays": float64(30)},
	})
	assets := testutil.NewAssetRepo()
	blobs := testutil.NewBlobStore()
	blobRepo := testutil.NewBlobStoreRepo()
	repos := testutil.NewRepoRepo() // no repos attach this policy

	svc := service.NewCleanupService(policies, repos, assets, blobRepo, blobs, nopLog())
	res, err := svc.RunPolicyResult(context.Background(), "orphan")
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.Skipped)
	assert.Contains(t, res.SkippedReason, "not attached")
	assert.Equal(t, 0, res.Deleted)
}

// RunPolicyResult returns counts for a normal age-based policy too.
func TestRunPolicyResult_ReturnsCounts(t *testing.T) {
	policies := testutil.NewCleanupPolicyRepo(&domain.CleanupPolicy{
		ID: "age", Name: "age", Enabled: true, Format: "maven2",
		Criteria: map[string]any{"artifactAgeDays": float64(30)},
	})
	assets := testutil.NewAssetRepo()
	assets.Stale = []domain.Asset{{ID: "a1", BlobKey: "bk1", SizeBytes: 10, Path: "/a"}}
	blobs := testutil.NewBlobStore()
	_ = blobs.Put(context.Background(), "bk1", testutil.MakeReader("x"), 1)
	blobRepo := testutil.NewBlobStoreRepo()
	repos := testutil.NewRepoRepo(&domain.Repository{
		Name: "mvn", ID: "r1", Format: domain.FormatMaven2, CleanupPolicyIDs: []string{"age"},
	})

	svc := service.NewCleanupService(policies, repos, assets, blobRepo, blobs, nopLog())
	res, err := svc.RunPolicyResult(context.Background(), "age")
	require.NoError(t, err)
	assert.Equal(t, 1, res.Deleted)
	assert.Equal(t, int64(10), res.FreedBytes)
}

// #62 (S3): blobs must be deleted from the asset's own physical store, not the
// global default. With a resolver wired, the delete hits the resolved store.
func TestRunPolicy_DeletesFromResolvedStore(t *testing.T) {
	defaultStore := testutil.NewBlobStore()
	memberStore := testutil.NewBlobStore()
	_ = memberStore.Put(context.Background(), "bk1", testutil.MakeReader("x"), 1)

	policies := testutil.NewCleanupPolicyRepo(&domain.CleanupPolicy{
		ID: "s3", Name: "s3-clear", Enabled: true, Format: "*",
		Criteria: map[string]any{"artifactAgeDays": float64(1)},
	})
	assets := testutil.NewAssetRepo()
	// asset lives on a non-default store
	assets.Stale = []domain.Asset{{ID: "a1", BlobKey: "bk1", SizeBytes: 100, Path: "/x", BlobStoreID: "member-1"}}
	blobRepo := testutil.NewBlobStoreRepo(&domain.BlobStore{ID: "member-1", Name: "s3-store", Type: "s3"})
	repos := testutil.NewRepoRepo(&domain.Repository{
		Name: "mvn", ID: "r1", Format: domain.FormatMaven2, CleanupPolicyIDs: []string{"s3"},
	})

	svc := service.NewCleanupService(policies, repos, assets, blobRepo, defaultStore, nopLog()).
		WithResolver(testutil.NewFakeResolver(memberStore))
	_, err := svc.RunPolicyResult(context.Background(), "s3")
	require.NoError(t, err)

	assert.Contains(t, memberStore.Deleted, "bk1", "delete must hit the asset's physical store")
	assert.NotContains(t, defaultStore.Deleted, "bk1", "must not delete from the global default store")
}

// After deleting an asset, the now-orphaned component must be pruned too, or the
// browse view keeps showing empty rows and the repo looks "not cleaned" (#62).
func TestRunPolicy_PrunesOrphanComponents(t *testing.T) {
	policies := testutil.NewCleanupPolicyRepo(&domain.CleanupPolicy{
		ID: "clr", Name: "clear", Enabled: true, Format: "*", Criteria: map[string]any{},
	})
	assets := testutil.NewAssetRepo()
	assets.Stale = []domain.Asset{{ID: "a1", BlobKey: "bk1", SizeBytes: 10, Path: "/x", Repository: "debian"}}
	blobs := testutil.NewBlobStore()
	_ = blobs.Put(context.Background(), "bk1", testutil.MakeReader("x"), 1)
	blobRepo := testutil.NewBlobStoreRepo()
	comps := testutil.NewComponentRepo()
	repos := testutil.NewRepoRepo(&domain.Repository{
		Name: "debian", ID: "r1", Format: domain.FormatApt, CleanupPolicyIDs: []string{"clr"},
	})

	svc := service.NewCleanupService(policies, repos, assets, blobRepo, blobs, nopLog()).
		WithComponents(comps)
	_, err := svc.RunPolicyResult(context.Background(), "clr")
	require.NoError(t, err)

	assert.Contains(t, comps.DeleteOrphansCalls, "debian", "orphan components must be pruned for the affected repo")
}

// A dry run must not prune components (it deletes nothing).
func TestRunPolicy_DryRun_DoesNotPruneOrphans(t *testing.T) {
	policies := testutil.NewCleanupPolicyRepo(&domain.CleanupPolicy{
		ID: "dry", Name: "dry", Enabled: true, Format: "*", DryRun: true,
		Criteria: map[string]any{"artifactAgeDays": float64(1)},
	})
	assets := testutil.NewAssetRepo()
	assets.Stale = []domain.Asset{{ID: "a1", BlobKey: "bk1", SizeBytes: 10, Path: "/x", Repository: "debian"}}
	blobs := testutil.NewBlobStore()
	blobRepo := testutil.NewBlobStoreRepo()
	comps := testutil.NewComponentRepo()
	repos := testutil.NewRepoRepo(&domain.Repository{
		Name: "debian", ID: "r1", Format: domain.FormatApt, CleanupPolicyIDs: []string{"dry"},
	})

	svc := service.NewCleanupService(policies, repos, assets, blobRepo, blobs, nopLog()).
		WithComponents(comps)
	_, err := svc.RunPolicyResult(context.Background(), "dry")
	require.NoError(t, err)

	assert.Empty(t, comps.DeleteOrphansCalls, "dry run must not prune components")
}

// A dry run must not re-process the same assets forever. Since dry runs delete
// nothing, ListStale keeps matching the same rows; the loop must stop after one
// batch instead of looping (previously it inflated counts into the millions).
func TestRunPolicy_DryRun_TerminatesAndCountsOnce(t *testing.T) {
	policies := testutil.NewCleanupPolicyRepo(&domain.CleanupPolicy{
		ID: "dry", Name: "dry", Enabled: true, Format: "*", DryRun: true,
		Criteria: map[string]any{"artifactAgeDays": float64(1)},
	})
	assets := testutil.NewAssetRepo()
	assets.StaleRepeat = true // simulate real DB: rows persist across queries in dry run
	assets.Stale = []domain.Asset{
		{ID: "a1", BlobKey: "bk1", SizeBytes: 10, Path: "/a"},
		{ID: "a2", BlobKey: "bk2", SizeBytes: 20, Path: "/b"},
	}
	blobs := testutil.NewBlobStore()
	blobRepo := testutil.NewBlobStoreRepo()
	repos := testutil.NewRepoRepo(&domain.Repository{
		Name: "mvn", ID: "r1", Format: domain.FormatMaven2, CleanupPolicyIDs: []string{"dry"},
	})

	svc := service.NewCleanupService(policies, repos, assets, blobRepo, blobs, nopLog())
	res, err := svc.RunPolicyResult(context.Background(), "dry")
	require.NoError(t, err)

	assert.Equal(t, 2, res.Deleted, "each stale asset counted exactly once")
	assert.Equal(t, int64(30), res.FreedBytes)
	assert.True(t, res.DryRun)
	assert.Equal(t, 1, assets.ListStaleCalls, "dry run must do a single pass, not loop")
	assert.Empty(t, blobs.Deleted, "dry run deletes nothing")
}

// Run stats are persisted via RecordRun, not the general Update. This keeps the
// form-edit path (which carries no run stats) from wiping last-run info — and is
// why the DB previously showed a policy as "never run" after it had run (#62).
func TestRunPolicy_RecordsRunStats_NotViaUpdate(t *testing.T) {
	policies := testutil.NewCleanupPolicyRepo(&domain.CleanupPolicy{
		ID: "p", Name: "p", Enabled: true, Format: "*", Criteria: map[string]any{},
	})
	assets := testutil.NewAssetRepo()
	assets.Stale = []domain.Asset{{ID: "a1", BlobKey: "bk1", SizeBytes: 7, Path: "/x"}}
	blobs := testutil.NewBlobStore()
	_ = blobs.Put(context.Background(), "bk1", testutil.MakeReader("x"), 1)
	blobRepo := testutil.NewBlobStoreRepo()
	repos := testutil.NewRepoRepo(&domain.Repository{
		Name: "r", ID: "r1", Format: domain.FormatRaw, CleanupPolicyIDs: []string{"p"},
	})

	svc := service.NewCleanupService(policies, repos, assets, blobRepo, blobs, nopLog())
	_, err := svc.RunPolicyResult(context.Background(), "p")
	require.NoError(t, err)

	require.Len(t, policies.RunRecords, 1)
	assert.Equal(t, 1, policies.RunRecords[0].Count)
	assert.Equal(t, int64(7), policies.RunRecords[0].Freed)
	assert.Empty(t, policies.Updates, "run stats must not go through the config Update path")
}
