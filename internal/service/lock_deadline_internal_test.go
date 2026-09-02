package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/storage"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// Nothing renews a distributed lock, so a run that outlives its TTL is no longer
// exclusive: another node can acquire the same lock and start the same work
// (#371). Each long-running job therefore stops at its own deadline. The real
// TTLs are 30 minutes and 2 hours, so these tests drive the unexported entry
// points with a deadline that has already passed.

func expiredDeadline() time.Time { return time.Now().Add(-time.Minute) }
func futureDeadline() time.Time  { return time.Now().Add(time.Hour) }

func deadlineCleanupService(assets *testutil.AssetRepo) (*CleanupService, *testutil.CleanupPolicyRepo) {
	policies := testutil.NewCleanupPolicyRepo(&domain.CleanupPolicy{
		ID: "p", Name: "p", Enabled: true, Format: "maven2",
		Criteria: map[string]any{"artifactAgeDays": float64(30)},
	})
	repos := testutil.NewRepoRepo(&domain.Repository{
		Name: "mvn", ID: "r1", Format: domain.FormatMaven2, CleanupPolicyIDs: []string{"p"},
	})
	svc := NewCleanupService(policies, repos, assets, testutil.NewBlobStoreRepo(), testutil.NewBlobStore(), zap.NewNop().Sugar())
	return svc, policies
}

func TestCleanup_RunPolicy_AbortsAtLockDeadline(t *testing.T) {
	assets := testutil.NewAssetRepo()
	assets.Stale = []domain.Asset{{ID: "a1", Repository: "mvn", BlobKey: "bk1", SizeBytes: 10, Path: "/a.jar"}}
	svc, policies := deadlineCleanupService(assets)

	p, err := policies.Get(context.Background(), "p")
	require.NoError(t, err)

	res, err := svc.runPolicy(context.Background(), *p, expiredDeadline())
	require.NoError(t, err)
	assert.True(t, res.Aborted, "a run past its lock TTL must stop instead of deleting unprotected")
	assert.Equal(t, 0, res.Deleted, "no further work is attempted after the deadline")
	// Partial results are still recorded, so the operator sees what the run did.
	assert.Len(t, policies.RunRecords, 1)
}

func TestCleanup_RunPolicy_RunsNormallyBeforeDeadline(t *testing.T) {
	assets := testutil.NewAssetRepo()
	assets.Stale = []domain.Asset{{ID: "a1", Repository: "mvn", BlobKey: "bk1", SizeBytes: 10, Path: "/a.jar"}}
	svc, policies := deadlineCleanupService(assets)

	p, err := policies.Get(context.Background(), "p")
	require.NoError(t, err)

	res, err := svc.runPolicy(context.Background(), *p, futureDeadline())
	require.NoError(t, err)
	assert.False(t, res.Aborted)
	assert.Equal(t, 1, res.Deleted)
}

func TestGC_Compact_AbortsAtLockDeadline(t *testing.T) {
	ctx := context.Background()
	store := testutil.NewBlobStore()
	require.NoError(t, store.Put(ctx, "orphan-1", testutil.MakeReader("xxxx"), 4))

	svc := &BlobGCService{Assets: testutil.NewAssetRepo(), Stores: testutil.NewBlobStoreRepo()}
	referenced, err := svc.referencedSet(ctx)
	require.NoError(t, err)

	res := svc.compact(ctx, "default", "store-1", store, referenced, GCOptions{}, expiredDeadline())
	assert.True(t, res.Aborted, "a pass past the GC lock's TTL must stop")
	assert.Equal(t, 0, res.Orphans, "no orphan is deleted after the deadline")
	assert.Empty(t, store.Deleted)

	res = svc.compact(ctx, "default", "store-1", store, referenced, GCOptions{}, futureDeadline())
	assert.False(t, res.Aborted)
	assert.Equal(t, 1, res.Orphans, "the ordinary, well-within-TTL pass is unchanged")
}

func TestBlobStoreMigration_RunMigration_AbortsAtLockDeadline(t *testing.T) {
	ctx := context.Background()
	srcDir, dstDir := t.TempDir(), t.TempDir()
	source := &domain.BlobStore{ID: "src", Name: "source", Type: "local", Config: map[string]any{"path": srcDir}}
	target := &domain.BlobStore{ID: "tgt", Name: "target", Type: "local", Config: map[string]any{"path": dstDir}}

	srcID := source.ID
	repos := testutil.NewRepoRepo(&domain.Repository{
		ID: "repo-1", Name: "my-repo", Format: domain.RepoFormat("raw"),
		Type: domain.TypeHosted, Online: true, BlobStoreID: &srcID,
	})
	assets := testutil.NewAssetRepo()
	assets.MigrationRows = []domain.MigrationAssetRow{
		{BlobKey: "bk1", SizeBytes: 4, SourceBlobStoreID: source.ID},
	}
	migs := testutil.NewBlobStoreMigrationRepo()
	defaultStore, err := storage.NewLocalBlobStore(srcDir)
	require.NoError(t, err)

	svc := NewBlobStoreMigrationService(migs, assets, repos, testutil.NewBlobStoreRepo(source, target), storage.NewRegistry(defaultStore))
	m := &domain.BlobStoreMigration{RepositoryName: "my-repo", SourceStoreID: source.ID, TargetStoreID: target.ID, Status: "pending"}
	require.NoError(t, migs.Create(ctx, m))

	svc.runMigration(ctx, m, nil, expiredDeadline())

	got, err := migs.GetLatestByRepo(ctx, "my-repo")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "cancelled", got.Status, //nolint:misspell // API/DB status value consumed by frontend
		"two migrations interleaving their asset repointing is the one case here that is not merely duplicate work")
	require.NotNil(t, got.ErrorMessage)
	assert.Contains(t, *got.ErrorMessage, "exceeded the lock's TTL")
}
