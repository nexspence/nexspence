package service_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/distlock"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
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

// ── Lock lifecycle ───────────────────────────────────────────

// migLocker records the keys it was asked for and can fail every acquisition,
// standing in for a lock held by another node.
type migLocker struct {
	mu       sync.Mutex
	err      error
	acquired []string
	forced   []string
	released int
}

func (lk *migLocker) Acquire(_ context.Context, key string, _ time.Duration) (distlock.Lock, error) {
	lk.mu.Lock()
	defer lk.mu.Unlock()
	lk.acquired = append(lk.acquired, key)
	if lk.err != nil {
		return nil, lk.err
	}
	return &migLock{owner: lk}, nil
}

func (lk *migLocker) ForceRelease(_ context.Context, key string) error {
	lk.mu.Lock()
	defer lk.mu.Unlock()
	lk.forced = append(lk.forced, key)
	return nil
}

func (lk *migLocker) forcedKeys() []string {
	lk.mu.Lock()
	defer lk.mu.Unlock()
	return append([]string(nil), lk.forced...)
}

func (lk *migLocker) releases() int {
	lk.mu.Lock()
	defer lk.mu.Unlock()
	return lk.released
}

type migLock struct{ owner *migLocker }

func (l *migLock) Release(context.Context) error {
	l.owner.mu.Lock()
	defer l.owner.mu.Unlock()
	l.owner.released++
	return nil
}

func seedMigrationRepo(t *testing.T, repoRepo *testutil.RepoRepo, blobStoreRepo *testutil.BlobStoreRepo, name string) string {
	t.Helper()
	ctx := context.Background()
	src := &domain.BlobStore{ID: "bs-src-" + name, Name: "source-" + name}
	dst := &domain.BlobStore{ID: "bs-dst-" + name, Name: "dest-" + name}
	require.NoError(t, blobStoreRepo.Create(ctx, src))
	require.NoError(t, blobStoreRepo.Create(ctx, dst))

	repoRec := &domain.Repository{Name: name, Format: "raw", Type: "hosted"}
	repoRec.BlobStoreID = &src.ID
	require.NoError(t, repoRepo.Create(ctx, repoRec))
	return dst.ID
}

// Losing the lock race must leave nothing behind: the migration row is only
// created once this node actually owns the lock, or the repo is wedged as
// "already active" for every later Start until the process restarts.
func TestBlobStoreMigration_Start_LockHeld_LeavesNoMigrationRow(t *testing.T) {
	svc, migRepo, repoRepo, blobStoreRepo := newMigSvc(t)
	ctx := context.Background()
	dstID := seedMigrationRepo(t, repoRepo, blobStoreRepo, "locked-repo")
	svc.WithLocker(&migLocker{err: distlock.ErrLockHeld})

	_, err := svc.Start(ctx, "locked-repo", dstID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "another node")

	_, err = migRepo.GetActiveByRepo(ctx, "locked-repo")
	assert.ErrorIs(t, err, repository.ErrNotFound, "no migration row may be left behind by a lost lock race")

	// And the repo is not wedged: once the lock frees up, Start works again.
	svc.WithLocker(&migLocker{})
	m, err := svc.Start(ctx, "locked-repo", dstID)
	require.NoError(t, err)
	require.NotNil(t, m)
}

// Same for a lock backend that is simply down.
func TestBlobStoreMigration_Start_LockError_LeavesNoMigrationRow(t *testing.T) {
	svc, migRepo, repoRepo, blobStoreRepo := newMigSvc(t)
	ctx := context.Background()
	dstID := seedMigrationRepo(t, repoRepo, blobStoreRepo, "errored-repo")
	svc.WithLocker(&migLocker{err: errors.New("redis down")})

	_, err := svc.Start(ctx, "errored-repo", dstID)
	require.Error(t, err)

	_, err = migRepo.GetActiveByRepo(ctx, "errored-repo")
	assert.ErrorIs(t, err, repository.ErrNotFound, "no migration row may be left behind when the lock backend fails")
}

// A migration row that cannot be created must not leave its lock held for the
// full 2h TTL either.
func TestBlobStoreMigration_Start_CreateFails_ReleasesLock(t *testing.T) {
	svc, migRepo, repoRepo, blobStoreRepo := newMigSvc(t)
	ctx := context.Background()
	dstID := seedMigrationRepo(t, repoRepo, blobStoreRepo, "create-fails")
	migRepo.CreateErr = errors.New("db down")

	lk := &migLocker{}
	svc.WithLocker(lk)

	_, err := svc.Start(ctx, "create-fails", dstID)
	require.Error(t, err)
	assert.Equal(t, 1, lk.releases(), "the lock taken for a migration that never started is given back")
}

// ResumeAll tells the operator an interrupted migration was canceled, so the
// lock the crashed process left behind has to go with it — otherwise Start
// keeps failing with "already running on another node" for up to 2 hours.
func TestBlobStoreMigration_ResumeAll_ReleasesStaleLocks(t *testing.T) {
	svc, migRepo, _, _ := newMigSvc(t)
	ctx := context.Background()

	require.NoError(t, migRepo.Create(ctx, &domain.BlobStoreMigration{RepositoryName: "repo1", Status: "running"}))
	require.NoError(t, migRepo.Create(ctx, &domain.BlobStoreMigration{RepositoryName: "repo2", Status: "pending"}))

	lk := &migLocker{}
	svc.WithLocker(lk)

	require.NoError(t, svc.ResumeAll(ctx))

	assert.ElementsMatch(t,
		[]string{"nexspence:lock:blobmig:repo1", "nexspence:lock:blobmig:repo2"},
		lk.forcedKeys(),
		"every migration ResumeAll gives up gives its lock back")
}

// A finished migration must leave nothing behind in the source: the copy there
// is unreferenced, and GC can no longer see it as an orphan by key alone. Before
// #297 the bytes stayed on disk and in the source's used_bytes forever — so a
// migration off a full store freed nothing.
func TestBlobStoreMigration_DropsSourceCopyAndUsage(t *testing.T) {
	ctx := context.Background()
	sourceID, targetID := "store-src-001", "store-tgt-002"
	srcDir, dstDir := t.TempDir(), t.TempDir()

	sourceMeta := &domain.BlobStore{ID: sourceID, Name: "source-store", Type: "local", Config: map[string]any{"path": srcDir}}
	targetMeta := &domain.BlobStore{ID: targetID, Name: "target-store", Type: "local", Config: map[string]any{"path": dstDir}}

	bsID := sourceID
	repo := &domain.Repository{ID: "repo-001", Name: "my-repo", Format: domain.RepoFormat("raw"),
		Type: domain.TypeHosted, Online: true, BlobStoreID: &bsID}

	srcStore, err := storage.NewLocalBlobStore(srcDir)
	require.NoError(t, err)
	dstStore, err := storage.NewLocalBlobStore(dstDir)
	require.NoError(t, err)
	require.NoError(t, srcStore.Put(ctx, "blob-1", strings.NewReader("data"), 4))

	blobRepo := testutil.NewBlobStoreRepo(sourceMeta, targetMeta)
	require.NoError(t, blobRepo.UpdateUsedBytes(ctx, "source-store", 4))

	assetRepo := testutil.NewAssetRepo()
	require.NoError(t, assetRepo.Create(ctx, &domain.Asset{
		ComponentID: "c1", RepositoryID: repo.ID, Repository: "my-repo",
		Path: "/data.bin", BlobKey: "blob-1", BlobStoreID: sourceID, SizeBytes: 4,
	}))
	assetRepo.MigrationRows = []domain.MigrationAssetRow{
		{BlobKey: "blob-1", SourceBlobStoreID: sourceID, SizeBytes: 4},
	}

	svc := service.NewBlobStoreMigrationService(testutil.NewBlobStoreMigrationRepo(), assetRepo,
		testutil.NewRepoRepo(repo), blobRepo, storage.NewRegistry(srcStore))

	_, err = svc.Start(ctx, "my-repo", targetID)
	require.NoError(t, err)
	waitForMigration(t, svc, "my-repo", "done")

	inTarget, err := dstStore.Exists(ctx, "blob-1")
	require.NoError(t, err)
	require.True(t, inTarget, "target must hold the migrated blob")

	inSource, err := srcStore.Exists(ctx, "blob-1")
	require.NoError(t, err)
	require.False(t, inSource, "source copy must be deleted once nothing references it there")

	src, err := blobRepo.Get(ctx, "source-store")
	require.NoError(t, err)
	require.Equal(t, int64(0), src.UsedBytes, "source usage must drop by the migrated bytes")

	dst, err := blobRepo.Get(ctx, "target-store")
	require.NoError(t, err)
	require.Equal(t, int64(4), dst.UsedBytes)
}

// The same key can still be in use on the source by another repository's asset
// (or an OCI digest alias). Then the physical blob stays, and so does the usage.
func TestBlobStoreMigration_KeepsSourceCopyStillReferencedThere(t *testing.T) {
	ctx := context.Background()
	sourceID, targetID := "store-src-001", "store-tgt-002"
	srcDir, dstDir := t.TempDir(), t.TempDir()

	sourceMeta := &domain.BlobStore{ID: sourceID, Name: "source-store", Type: "local", Config: map[string]any{"path": srcDir}}
	targetMeta := &domain.BlobStore{ID: targetID, Name: "target-store", Type: "local", Config: map[string]any{"path": dstDir}}

	bsID := sourceID
	repo := &domain.Repository{ID: "repo-001", Name: "my-repo", Format: domain.RepoFormat("raw"),
		Type: domain.TypeHosted, Online: true, BlobStoreID: &bsID}

	srcStore, err := storage.NewLocalBlobStore(srcDir)
	require.NoError(t, err)
	require.NoError(t, srcStore.Put(ctx, "blob-1", strings.NewReader("data"), 4))

	blobRepo := testutil.NewBlobStoreRepo(sourceMeta, targetMeta)
	require.NoError(t, blobRepo.UpdateUsedBytes(ctx, "source-store", 4))

	assetRepo := testutil.NewAssetRepo()
	require.NoError(t, assetRepo.Create(ctx, &domain.Asset{
		ComponentID: "c1", RepositoryID: repo.ID, Repository: "my-repo",
		Path: "/data.bin", BlobKey: "blob-1", BlobStoreID: sourceID, SizeBytes: 4,
	}))
	// Another repository shares the key on the source store; it is not migrated.
	require.NoError(t, assetRepo.Create(ctx, &domain.Asset{
		ComponentID: "c2", RepositoryID: "repo-002", Repository: "other-repo",
		Path: "/data.bin", BlobKey: "blob-1", BlobStoreID: sourceID, SizeBytes: 4,
	}))
	assetRepo.MigrationRows = []domain.MigrationAssetRow{
		{BlobKey: "blob-1", SourceBlobStoreID: sourceID, SizeBytes: 4},
	}

	svc := service.NewBlobStoreMigrationService(testutil.NewBlobStoreMigrationRepo(), assetRepo,
		testutil.NewRepoRepo(repo), blobRepo, storage.NewRegistry(srcStore))

	_, err = svc.Start(ctx, "my-repo", targetID)
	require.NoError(t, err)
	waitForMigration(t, svc, "my-repo", "done")

	inSource, err := srcStore.Exists(ctx, "blob-1")
	require.NoError(t, err)
	require.True(t, inSource, "a copy another asset still points at must survive")

	src, err := blobRepo.Get(ctx, "source-store")
	require.NoError(t, err)
	require.Equal(t, int64(4), src.UsedBytes)
}
