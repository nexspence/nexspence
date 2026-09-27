package service_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/storage"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// A backup or restore must never report success while bytes went missing
// (#490 review): these cover the read side (Export), the write side
// (Restore/ImportRepo) and ImportRepo into a repository on a group store.

// mapResolver resolves each blob store id to its own store; an unknown id is
// a resolution failure, like a store whose credentials no longer work.
type mapResolver map[string]storage.BlobStore

func (m mapResolver) Get(_ context.Context, desc storage.BlobStoreDescriptor) (storage.BlobStore, error) {
	if s, ok := m[desc.ID]; ok {
		return s, nil
	}
	return nil, fmt.Errorf("resolver: no store for %s", desc.ID)
}

// getRefusingStore refuses every read, as an S3 store does after its
// credentials are rotated.
type getRefusingStore struct{ *testutil.BlobStore }

func (getRefusingStore) Get(context.Context, string) (io.ReadCloser, int64, error) {
	return nil, 0, errors.New("s3: 403 InvalidAccessKeyId")
}

// putRefusingStore refuses every write, as unreachable storage does.
type putRefusingStore struct{ *testutil.BlobStore }

func (putRefusingStore) Put(context.Context, string, io.Reader, int64) error {
	return errors.New("s3: connection refused")
}

// seedAsset creates a component and an asset for key on blobStoreID.
func seedAsset(t *testing.T, svc *service.BackupService, repo *domain.Repository, path, key, blobStoreID string) {
	t.Helper()
	ctx := context.Background()
	comp := &domain.Component{RepositoryID: repo.ID, Repository: repo.Name, Format: "raw", Name: path, Version: "1"}
	require.NoError(t, svc.Components.Create(ctx, comp))
	require.NoError(t, svc.Assets.Create(ctx, &domain.Asset{
		ComponentID: comp.ID, RepositoryID: repo.ID, Repository: repo.Name,
		Path: path, BlobKey: key, BlobStoreID: blobStoreID, SizeBytes: 1,
	}))
}

func TestValidateSchedule_RejectsSubMinuteIntervals(t *testing.T) {
	for _, bad := range []string{"@every 10s", "@every 59s", "@every 500ms"} {
		assert.Error(t, service.ValidateSchedule(bad), bad)
	}
	for _, ok := range []string{"@every 1m", "@every 90s", "@hourly", "*/1 * * * *"} {
		assert.NoError(t, service.ValidateSchedule(ok), ok)
	}
}

// Rotated S3 credentials: every read on that store fails. The archive is
// still written with everything else, but the export says it is incomplete.
func TestBackup_Export_UnreadableStore_ReportsIncomplete(t *testing.T) {
	ctx := context.Background()
	repo := testutil.SimpleRepo("mixed", "raw")
	svc := buildBackupSvc(repo)
	good := testutil.NewBlobStore()
	svc.Resolver = mapResolver{
		"bs-local": good,
		"bs-s3":    getRefusingStore{testutil.NewBlobStore()},
	}
	require.NoError(t, svc.BlobStores.Create(ctx, &domain.BlobStore{ID: "bs-local", Name: "default", Type: "local"}))
	require.NoError(t, svc.BlobStores.Create(ctx, &domain.BlobStore{ID: "bs-s3", Name: "s3", Type: "s3"}))
	require.NoError(t, good.Put(ctx, "aa/bb/ok", bytes.NewReader([]byte("x")), 1))
	seedAsset(t, svc, repo, "/ok", "aa/bb/ok", "bs-local")
	seedAsset(t, svc, repo, "/s3-1", "cc/dd/s3-1", "bs-s3")
	seedAsset(t, svc, repo, "/s3-2", "cc/dd/s3-2", "bs-s3")

	var buf bytes.Buffer
	var incomplete *service.IncompleteBackupError
	require.ErrorAs(t, svc.Export(ctx, &buf), &incomplete)
	assert.Equal(t, 2, incomplete.Missing)
	assert.Contains(t, incomplete.Error(), "InvalidAccessKeyId", "the first failure must say why")

	// The archive is sound: it restores, with the blob that was readable.
	dst := buildBackupSvc()
	stats, err := dst.Restore(ctx, &buf)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.Blobs)
	assert.Equal(t, 3, stats.Assets)
}

// A store that no longer resolves must not fall back to the default store:
// the blobs are counted as missing instead of silently read from elsewhere.
func TestBackup_Export_UnresolvableStore_ReportsIncomplete(t *testing.T) {
	ctx := context.Background()
	repo := testutil.SimpleRepo("gone", "raw")
	svc := buildBackupSvc(repo)
	svc.Resolver = mapResolver{} // resolves nothing
	require.NoError(t, svc.BlobStores.Create(ctx, &domain.BlobStore{ID: "bs-s3", Name: "s3", Type: "s3"}))
	// Same key in the fallback store: reading it would be the old silent bug.
	require.NoError(t, svc.BlobStore.Put(ctx, "cc/dd/k", bytes.NewReader([]byte("wrong store")), 11))
	seedAsset(t, svc, repo, "/k", "cc/dd/k", "bs-s3")

	var buf bytes.Buffer
	var incomplete *service.IncompleteBackupError
	require.ErrorAs(t, svc.Export(ctx, &buf), &incomplete)
	assert.Equal(t, 1, incomplete.Missing)
}

// An incomplete scheduled backup is kept (it still beats nothing) but is a
// failed run, and retention is not applied, so it never prunes the last
// complete backup.
func TestBackupService_RunScheduled_IncompleteBackup_KeptFailedAndNoRetention(t *testing.T) {
	ctx := context.Background()
	repo := testutil.SimpleRepo("r1", "raw")
	svc, settings := buildScheduledBackupSvc(repo)
	dest := testutil.NewBlobStore()
	svc.Resolver = mapResolver{
		"bs-dest": dest,
		"bs-s3":   getRefusingStore{testutil.NewBlobStore()},
	}
	require.NoError(t, svc.BlobStores.Create(ctx, &domain.BlobStore{ID: "bs-dest", Name: "backup-dest", Type: "local"}))
	require.NoError(t, svc.BlobStores.Create(ctx, &domain.BlobStore{ID: "bs-s3", Name: "s3", Type: "s3"}))
	seedAsset(t, svc, repo, "/s3", "cc/dd/s3", "bs-s3")
	for _, k := range []string{"backups/nexspence-backup-20260101-030000.tar.gz", "backups/nexspence-backup-20260102-030000.tar.gz"} {
		require.NoError(t, dest.Put(ctx, k, bytes.NewReader([]byte("old")), 3))
	}
	require.NoError(t, settings.Upsert(ctx, &domain.BackupSettings{
		Enabled: true, ScheduleCron: "0 3 * * *", BlobStoreID: "bs-dest", RetentionCount: 1,
	}))

	key, err := svc.RunScheduled(ctx)
	var incomplete *service.IncompleteBackupError
	require.ErrorAs(t, err, &incomplete)
	assert.Contains(t, err.Error(), key, "the error names the archive that was kept")
	require.NotEmpty(t, key)
	assert.True(t, dest.Has(key), "the incomplete archive is still written")

	entries, err := dest.ListEntries(ctx)
	require.NoError(t, err)
	assert.Len(t, entries, 3, "retention must not run after an incomplete backup")
}

// Unreachable storage on restore: the blob is counted as failed and its
// asset is not created, instead of "Restore complete, 1 blob".
func TestBackup_Restore_BlobWriteFailure_CountedAndAssetSkipped(t *testing.T) {
	ctx := context.Background()
	buf := exportWithS3Repo(t)

	dst := buildBackupSvc()
	dst.Resolver = testutil.NewFakeResolver(putRefusingStore{testutil.NewBlobStore()})
	stats, err := dst.Restore(ctx, buf)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.BlobsFailed)
	assert.Equal(t, 0, stats.Blobs)
	assert.Equal(t, 0, stats.Assets)

	asset, _ := dst.Assets.GetByPath(ctx, "s3repo", "/f.txt")
	assert.Nil(t, asset, "an asset whose bytes were not written must not be restored")
}

// exportRepoArchive exports a one-asset repository "imp" from the default store.
func exportRepoArchive(t *testing.T) *bytes.Buffer {
	t.Helper()
	ctx := context.Background()
	repo := testutil.SimpleRepo("imp", "raw")
	src := buildBackupSvc(repo)
	require.NoError(t, src.BlobStore.Put(ctx, "ee/ff/imp", bytes.NewReader([]byte("payload")), 7))
	seedAsset(t, src, repo, "/imp.bin", "ee/ff/imp", "")
	var buf bytes.Buffer
	require.NoError(t, src.ExportRepo(ctx, "imp", &buf))
	return &buf
}

func TestBackup_ImportRepo_BlobWriteFailure_CountedAndAssetSkipped(t *testing.T) {
	ctx := context.Background()
	buf := exportRepoArchive(t)

	dst := buildBackupSvc()
	dst.Resolver = testutil.NewFakeResolver(putRefusingStore{testutil.NewBlobStore()})
	require.NoError(t, dst.BlobStores.Create(ctx, &domain.BlobStore{ID: "bs-default", Name: "default", Type: "local"}))
	stats, err := dst.ImportRepo(ctx, buf, "", "skip")
	require.NoError(t, err)
	assert.Equal(t, 1, stats.BlobsFailed)
	assert.Equal(t, 0, stats.Assets)
	assert.Equal(t, 0, stats.Blobs)
}

// A repository on a group store: the bytes and the asset row must both land
// on a physical member (the first with capacity), not the default store with
// the group's id on the row.
func TestBackup_ImportRepo_GroupStore_WritesToFirstMemberWithCapacity(t *testing.T) {
	ctx := context.Background()
	buf := exportRepoArchive(t)

	dst := buildBackupSvc()
	member := testutil.NewBlobStore()
	dst.Resolver = mapResolver{"bs-full": testutil.NewBlobStore(), "bs-m2": member}
	quota := int64(10)
	require.NoError(t, dst.BlobStores.Create(ctx, &domain.BlobStore{ID: "bs-full", Name: "full", Type: "local", QuotaBytes: &quota, UsedBytes: 10}))
	require.NoError(t, dst.BlobStores.Create(ctx, &domain.BlobStore{ID: "bs-m2", Name: "m2", Type: "local"}))
	require.NoError(t, dst.BlobStores.Create(ctx, &domain.BlobStore{
		ID: "bs-group", Name: "group", Type: "group",
		Config: map[string]any{"member_ids": []any{"bs-full", "bs-m2"}, "fill_policy": "write_to_first_fill"},
	}))
	groupID := "bs-group"
	target := testutil.SimpleRepo("imp", "raw")
	target.BlobStoreID = &groupID
	require.NoError(t, dst.Repos.Create(ctx, target))

	stats, err := dst.ImportRepo(ctx, buf, "", "skip")
	require.NoError(t, err)
	assert.Equal(t, 1, stats.Assets)
	assert.Equal(t, 0, stats.BlobsFailed)

	asset, err := dst.Assets.GetByPath(ctx, "imp", "/imp.bin")
	require.NoError(t, err)
	require.NotNil(t, asset)
	assert.Equal(t, "bs-m2", asset.BlobStoreID, "the row must name the member that holds the bytes")
	assert.True(t, member.Has("ee/ff/imp"), "the bytes must be on that member")
	assert.False(t, dst.BlobStore.(*testutil.BlobStore).Has("ee/ff/imp"), "nothing may land in the default store")
}

func TestBackup_ImportRepo_GroupStoreWithNoRoom_IsAnError(t *testing.T) {
	ctx := context.Background()
	buf := exportRepoArchive(t)

	dst := buildBackupSvc()
	dst.Resolver = mapResolver{}
	quota := int64(10)
	require.NoError(t, dst.BlobStores.Create(ctx, &domain.BlobStore{ID: "bs-full", Name: "full", Type: "local", QuotaBytes: &quota, UsedBytes: 10}))
	require.NoError(t, dst.BlobStores.Create(ctx, &domain.BlobStore{
		ID: "bs-group", Name: "group", Type: "group",
		Config: map[string]any{"member_ids": []any{"bs-full"}},
	}))
	groupID := "bs-group"
	target := testutil.SimpleRepo("imp", "raw")
	target.BlobStoreID = &groupID
	require.NoError(t, dst.Repos.Create(ctx, target))

	_, err := dst.ImportRepo(ctx, buf, "", "skip")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no member")
}

// nameSortedStores lists blob stores by name, as the Postgres repository does.
type nameSortedStores struct{ *testutil.BlobStoreRepo }

func (s nameSortedStores) List(ctx context.Context) ([]domain.BlobStore, error) {
	all, err := s.BlobStoreRepo.List(ctx)
	sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })
	return all, err
}

// A repository created by the import has no store of its own, so later
// uploads go to the default store. The imported assets must go there too, not
// to whichever store sorts first by name (#549).
func TestBackup_ImportRepo_NewRepo_AssetsGoToTheDefaultStore(t *testing.T) {
	ctx := context.Background()
	buf := exportRepoArchive(t)

	dst := buildBackupSvc()
	backups := testutil.NewBlobStore()
	dst.Resolver = mapResolver{"bs-backups": backups, defaultStoreID: dst.BlobStore}
	stores := nameSortedStores{testutil.NewBlobStoreRepo()}
	require.NoError(t, stores.Create(ctx, &domain.BlobStore{ID: "bs-backups", Name: "backups", Type: "s3"}))
	dst.BlobStores = stores

	stats, err := dst.ImportRepo(ctx, buf, "imp-copy", "skip")
	require.NoError(t, err)
	assert.Equal(t, 1, stats.Assets)

	repo, err := dst.Repos.Get(ctx, "imp-copy")
	require.NoError(t, err)
	assert.Nil(t, repo.BlobStoreID, "an imported repository keeps using the default store")
	asset, err := dst.Assets.GetByPath(ctx, "imp-copy", "/imp.bin")
	require.NoError(t, err)
	require.NotNil(t, asset)
	assert.Equal(t, defaultStoreID, asset.BlobStoreID, "the asset must live where the repository's uploads go")
	assert.False(t, backups.Has("ee/ff/imp"), "nothing may land in a store the repository does not use")
}
