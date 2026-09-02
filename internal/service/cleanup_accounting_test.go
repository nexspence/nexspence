package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/storage"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// deleteFailStore is a store whose physical delete always fails — a full disk, a
// read-only shard directory, a backend timeout.
type deleteFailStore struct {
	*testutil.BlobStore
}

func (s deleteFailStore) Delete(_ context.Context, _ string) error {
	return errors.New("permission denied")
}

func accountingPolicy(id string) *domain.CleanupPolicy {
	return &domain.CleanupPolicy{
		ID: id, Name: id, Enabled: true, Format: "maven2",
		Criteria: map[string]any{"artifactAgeDays": float64(30)},
	}
}

func accountingRepos(policyID string) *testutil.RepoRepo {
	return testutil.NewRepoRepo(&domain.Repository{
		Name: "mvn", ID: "r1", Format: domain.FormatMaven2, CleanupPolicyIDs: []string{policyID},
	})
}

// One object can carry several assets (an OCI manifest's tag and its digest
// alias): expiring one of them deletes the row but must leave the bytes, since
// the sibling still points at them. The run used to count those bytes as freed
// anyway (#368).
func TestRunPolicyResult_SharedBlobKey_ReportsNoBytesFreed(t *testing.T) {
	policies := testutil.NewCleanupPolicyRepo(accountingPolicy("shared"))
	assets := testutil.NewAssetRepo()
	assets.Stale = []domain.Asset{{ID: "a1", Repository: "mvn", BlobKey: "bk-shared", SizeBytes: 49, Path: "/stale.jar"}}
	// The sibling asset that survives this run and keeps referencing the blob.
	assets.SeedAsset(&domain.Asset{
		ID: "a2", Repository: "mvn", BlobKey: "bk-shared", SizeBytes: 49, Path: "/live.jar",
	})
	blobs := testutil.NewBlobStore()
	require.NoError(t, blobs.Put(context.Background(), "bk-shared", testutil.MakeReader("x"), 1))

	svc := service.NewCleanupService(policies, accountingRepos("shared"), assets, testutil.NewBlobStoreRepo(), blobs, nopLog())
	res, err := svc.RunPolicyResult(context.Background(), "shared")
	require.NoError(t, err)

	assert.Equal(t, 1, res.Deleted, "the stale row is still removed")
	assert.Equal(t, int64(0), res.FreedBytes, "the bytes are still there — the sibling asset references them")
	assert.Empty(t, blobs.Deleted, "a blob a live asset points at must not be deleted")
	exists, err := blobs.Exists(context.Background(), "bk-shared")
	require.NoError(t, err)
	assert.True(t, exists)
}

// A physical delete that fails leaves the bytes on disk. The row is still gone
// (the DB row and the blob's own lifecycle are independent, and the blob GC
// reclaims the orphan later) but nothing was freed (#368).
func TestRunPolicyResult_PhysicalDeleteFails_ReportsNoBytesFreed(t *testing.T) {
	policies := testutil.NewCleanupPolicyRepo(accountingPolicy("failing"))
	assets := testutil.NewAssetRepo()
	assets.Stale = []domain.Asset{{ID: "a1", Repository: "mvn", BlobKey: "bk1", SizeBytes: 49, Path: "/stale.jar"}}
	inner := testutil.NewBlobStore()
	require.NoError(t, inner.Put(context.Background(), "bk1", testutil.MakeReader("x"), 1))
	var blobs storage.BlobStore = deleteFailStore{BlobStore: inner}

	svc := service.NewCleanupService(policies, accountingRepos("failing"), assets, testutil.NewBlobStoreRepo(), blobs, nopLog())
	res, err := svc.RunPolicyResult(context.Background(), "failing")
	require.NoError(t, err)

	assert.Equal(t, 1, res.Deleted)
	assert.Equal(t, int64(0), res.FreedBytes, "the delete failed — those bytes never left the store")
	exists, err := inner.Exists(context.Background(), "bk1")
	require.NoError(t, err)
	assert.True(t, exists, "the blob is still on disk")
}
