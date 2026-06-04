package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/storage"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func newMigSvc(t *testing.T) (*service.BlobStoreMigrationService, *testutil.BlobStoreMigrationRepo, *testutil.RepoRepo, *testutil.BlobStoreRepo) {
	t.Helper()
	migRepo := testutil.NewBlobStoreMigrationRepo()
	repoRepo := testutil.NewRepoRepo()
	blobStoreRepo := testutil.NewBlobStoreRepo()
	svc := service.NewBlobStoreMigrationService(
		migRepo,
		testutil.NewAssetRepo(),
		repoRepo,
		blobStoreRepo,
		storage.NewRegistry(testutil.NewBlobStore()),
	)
	return svc, migRepo, repoRepo, blobStoreRepo
}

func TestBlobStoreMigration_WithLocker_ReturnsSelf(t *testing.T) {
	svc, _, _, _ := newMigSvc(t) //nolint:dogsled
	got := svc.WithLocker(nil)
	assert.Equal(t, svc, got)
}

func TestBlobStoreMigration_Cancel_NoOp_WhenNotRunning(t *testing.T) {
	svc, _, _, _ := newMigSvc(t) //nolint:dogsled
	// Cancel a non-existent migration ID must not error.
	require.NoError(t, svc.Cancel(context.Background(), "no-such-id"))
}

func TestBlobStoreMigration_Cancel_SignalsRunningMigration(t *testing.T) {
	svc, _, repoRepo, blobStoreRepo := newMigSvc(t)
	ctx := context.Background()

	// Seed: a repo and two blob stores.
	src := &domain.BlobStore{ID: "bs-src", Name: "source"}
	dst := &domain.BlobStore{ID: "bs-dst", Name: "dest"}
	require.NoError(t, blobStoreRepo.Create(ctx, src))
	require.NoError(t, blobStoreRepo.Create(ctx, dst))

	repoRec := &domain.Repository{Name: "cancel-repo", Format: "raw", Type: "hosted"}
	repoRec.BlobStoreID = &src.ID
	require.NoError(t, repoRepo.Create(ctx, repoRec))

	m, err := svc.Start(ctx, "cancel-repo", dst.ID)
	require.NoError(t, err)
	require.NotNil(t, m)

	// Cancel must not error even mid-flight.
	require.NoError(t, svc.Cancel(ctx, m.ID))
}

func TestBlobStoreMigration_ResumeAll_MarksActiveAsCancelled(t *testing.T) {
	svc, migRepo, _, _ := newMigSvc(t)
	ctx := context.Background()

	// Seed two active migrations directly in the repo.
	m1 := &domain.BlobStoreMigration{RepositoryName: "repo1", Status: "running"}
	m2 := &domain.BlobStoreMigration{RepositoryName: "repo2", Status: "running"}
	require.NoError(t, migRepo.Create(ctx, m1))
	require.NoError(t, migRepo.Create(ctx, m2))

	require.NoError(t, svc.ResumeAll(ctx))

	// Both should now be in a terminal state (the mock FinishMigration sets status).
	got1, err := migRepo.GetLatestByRepo(ctx, "repo1")
	require.NoError(t, err)
	require.NotNil(t, got1)
	assert.NotEqual(t, "running", got1.Status)

	got2, err := migRepo.GetLatestByRepo(ctx, "repo2")
	require.NoError(t, err)
	require.NotNil(t, got2)
	assert.NotEqual(t, "running", got2.Status)
}

func TestBlobStoreMigration_Start_RepoNotFound(t *testing.T) {
	svc, _, _, _ := newMigSvc(t)
	_, err := svc.Start(context.Background(), "no-such-repo", "bs-1")
	require.Error(t, err)
}

func TestBlobStoreMigration_Start_TargetStoreNotFound(t *testing.T) {
	svc, _, repoRepo, _ := newMigSvc(t)
	ctx := context.Background()
	repoRec := &domain.Repository{Name: "repo-a", Format: "raw", Type: "hosted"}
	require.NoError(t, repoRepo.Create(ctx, repoRec))
	_, err := svc.Start(ctx, "repo-a", "nonexistent-store")
	require.Error(t, err)
}
