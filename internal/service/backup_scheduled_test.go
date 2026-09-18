package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/storage"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func buildScheduledBackupSvc(repos ...*domain.Repository) (*service.BackupService, *testutil.BackupSettingsRepo) {
	settings := testutil.NewBackupSettingsRepo()
	svc := &service.BackupService{
		BlobStores: testutil.NewBlobStoreRepo(),
		Repos:      testutil.NewRepoRepo(repos...),
		Users:      testutil.NewUserRepo(),
		Roles:      testutil.NewRoleRepo(),
		Policies:   testutil.NewCleanupPolicyRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
	}
	svc.WithSettings(settings)
	return svc, settings
}

func TestBackupService_RunScheduled_DisabledIsNoop(t *testing.T) {
	svc, _ := buildScheduledBackupSvc()
	key, err := svc.RunScheduled(context.Background())
	require.NoError(t, err)
	assert.Empty(t, key)
}

func TestBackupService_RunScheduled_NoDestinationIsNoop(t *testing.T) {
	ctx := context.Background()
	svc, settings := buildScheduledBackupSvc()
	require.NoError(t, settings.Upsert(ctx, &domain.BackupSettings{Enabled: true, ScheduleCron: "0 3 * * *", RetentionCount: 7}))

	key, err := svc.RunScheduled(ctx)
	require.NoError(t, err)
	assert.Empty(t, key, "enabled but no blob_store_id chosen yet must still be a no-op")
}

func TestBackupService_RunScheduled_WritesToConfiguredStore(t *testing.T) {
	ctx := context.Background()
	svc, settings := buildScheduledBackupSvc(testutil.SimpleRepo("r1", "raw"))

	dest := testutil.NewBlobStore()
	svc.Resolver = testutil.NewFakeResolver(dest)
	bs := &domain.BlobStore{ID: "bs-dest", Name: "backup-dest", Type: "s3"}
	require.NoError(t, svc.BlobStores.Create(ctx, bs))
	require.NoError(t, settings.Upsert(ctx, &domain.BackupSettings{
		Enabled: true, ScheduleCron: "0 3 * * *", BlobStoreID: bs.ID, RetentionCount: 7,
	}))

	key, err := svc.RunScheduled(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, key)
	assert.True(t, strings.HasPrefix(key, "backups/nexspence-backup-"))

	rc, size, err := dest.Get(ctx, key)
	require.NoError(t, err, "archive must land in the configured destination store")
	defer rc.Close()
	assert.Greater(t, size, int64(0))

	// RunScheduled itself is the pure "do the backup" primitive — recording
	// LastRun* is runOnce's job (the cron-triggered wrapper), so settings are
	// untouched by this direct call. Covered separately below.
	got, err := settings.Get(ctx)
	require.NoError(t, err)
	assert.Nil(t, got.LastRunAt)
}

func TestBackupService_RecordRun_PersistsAfterAScheduledRun(t *testing.T) {
	// Exercises the same Settings.RecordRun contract runOnce relies on,
	// without depending on unexported scheduling internals.
	ctx := context.Background()
	settings := testutil.NewBackupSettingsRepo()
	require.NoError(t, settings.Upsert(ctx, &domain.BackupSettings{Enabled: true, ScheduleCron: "0 3 * * *", RetentionCount: 7}))

	now := time.Now()
	require.NoError(t, settings.RecordRun(ctx, now, "backups/nexspence-backup-x.tar.gz", ""))

	got, err := settings.Get(ctx)
	require.NoError(t, err)
	require.NotNil(t, got.LastRunAt)
	assert.WithinDuration(t, now, *got.LastRunAt, time.Second)
	assert.Equal(t, "backups/nexspence-backup-x.tar.gz", got.LastRunKey)
	assert.Empty(t, got.LastRunError)
	// Settings the run recorded against must be untouched.
	assert.True(t, got.Enabled)
	assert.Equal(t, 7, got.RetentionCount)
}

// failingResolver always fails — used to prove a resolution failure surfaces
// as an error rather than silently falling back to the instance default.
type failingResolver struct{}

func (failingResolver) Get(context.Context, storage.BlobStoreDescriptor) (storage.BlobStore, error) {
	return nil, errors.New("resolver: simulated failure")
}

func TestBackupService_RunScheduled_ResolutionFailure_ReturnsErrorNotSilentFallback(t *testing.T) {
	// Regression test: a misconfigured/unresolvable destination store used to
	// fall back to svc.BlobStore (the instance default) via the same storeFor
	// helper Export/Restore use — silently writing scheduled backups to the
	// wrong place while still reporting success. Caught live: an S3 blob
	// store created with an empty config resolved to nothing, and two
	// scheduled backups landed on local disk instead of the configured MinIO
	// bucket, undetected until the destination bucket was inspected directly.
	ctx := context.Background()
	svc, settings := buildScheduledBackupSvc(testutil.SimpleRepo("r1", "raw"))
	svc.Resolver = failingResolver{}

	bs := &domain.BlobStore{ID: "bs-broken", Name: "broken-s3", Type: "s3"}
	require.NoError(t, svc.BlobStores.Create(ctx, bs))
	require.NoError(t, settings.Upsert(ctx, &domain.BackupSettings{
		Enabled: true, ScheduleCron: "0 3 * * *", BlobStoreID: bs.ID, RetentionCount: 7,
	}))

	key, err := svc.RunScheduled(ctx)
	require.Error(t, err, "an unresolvable destination must fail loudly, not silently use the default store")
	assert.Empty(t, key)

	// The default store must have received nothing — the whole point of the
	// assertion above.
	entries, lerr := svc.BlobStore.ListEntries(ctx)
	require.NoError(t, lerr)
	assert.Empty(t, entries, "the default store must never receive a scheduled backup on resolution failure")
}

func TestBackupService_ApplyRetention_KeepsOnlyNewest(t *testing.T) {
	ctx := context.Background()
	svc, settings := buildScheduledBackupSvc(testutil.SimpleRepo("r1", "raw"))

	dest := testutil.NewBlobStore()
	svc.Resolver = testutil.NewFakeResolver(dest)
	bs := &domain.BlobStore{ID: "bs-dest", Name: "backup-dest", Type: "s3"}
	require.NoError(t, svc.BlobStores.Create(ctx, bs))
	require.NoError(t, settings.Upsert(ctx, &domain.BackupSettings{
		Enabled: true, ScheduleCron: "0 3 * * *", BlobStoreID: bs.ID, RetentionCount: 2,
	}))

	// Run three times; each run's key is timestamp-suffixed to the second, so
	// force distinct timestamps rather than relying on real wall-clock ticks.
	var keys []string
	for i := 0; i < 3; i++ {
		key, err := svc.RunScheduled(ctx)
		require.NoError(t, err)
		keys = append(keys, key)
		time.Sleep(1100 * time.Millisecond) // cross a real second boundary for a distinct key
	}

	entries, err := dest.ListEntries(ctx)
	require.NoError(t, err)
	assert.Len(t, entries, 2, "only retentionCount backups should survive")

	// The oldest of the three must be gone.
	_, _, err = dest.Get(ctx, keys[0])
	assert.Error(t, err, "oldest backup beyond retention must have been deleted")
	_, _, err = dest.Get(ctx, keys[2])
	assert.NoError(t, err, "most recent backup must survive")
}

func TestBackupService_ReloadSchedule_BeforeStartIsNoop(t *testing.T) {
	svc, settings := buildScheduledBackupSvc()
	ctx := context.Background()
	require.NoError(t, settings.Upsert(ctx, &domain.BackupSettings{Enabled: true, ScheduleCron: "0 3 * * *"}))
	// StartScheduler was never called, so the cron.Cron doesn't exist yet —
	// ReloadSchedule must not panic, just no-op until StartScheduler runs.
	require.NoError(t, svc.ReloadSchedule(ctx))
}

func TestBackupService_ReloadSchedule_InvalidCronReturnsError(t *testing.T) {
	svc, settings := buildScheduledBackupSvc()
	bg := context.Background()
	require.NoError(t, settings.Upsert(bg, &domain.BackupSettings{Enabled: true, ScheduleCron: "not-a-cron-expression"}))

	ctx, cancel := context.WithCancel(bg)
	t.Cleanup(cancel)
	go svc.StartScheduler(ctx)
	// A short sleep lets StartScheduler initialize s.sched.cronScheduler
	// before this call.
	time.Sleep(50 * time.Millisecond)
	err := svc.ReloadSchedule(bg)
	assert.Error(t, err)
}
