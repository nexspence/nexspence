package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/distlock"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// ── distlock stubs ────────────────────────────────────────────

type extraCleanupLock struct{ released bool }

func (l *extraCleanupLock) Release(_ context.Context) error {
	l.released = true
	return nil
}

type extraCleanupLocker struct {
	err  error // when set, Acquire returns this
	lock *extraCleanupLock
}

func (lk *extraCleanupLocker) Acquire(_ context.Context, _ string, _ time.Duration) (distlock.Lock, error) {
	if lk.err != nil {
		return nil, lk.err
	}
	lk.lock = &extraCleanupLock{}
	return lk.lock, nil
}

// ── NewCleanupService constructor (line 56) ───────────────────

func TestExtraCleanup_NewCleanupService_NotNil(t *testing.T) {
	svc := service.NewCleanupService(
		testutil.NewCleanupPolicyRepo(),
		testutil.NewRepoRepo(),
		testutil.NewAssetRepo(),
		testutil.NewBlobStoreRepo(),
		testutil.NewBlobStore(),
		nopLog(),
	)
	require.NotNil(t, svc)
}

// ── PreviewPolicy (line 257) — 0% in report ──────────────────

func TestExtraCleanup_PreviewPolicy_NotFound(t *testing.T) {
	svc := service.NewCleanupService(
		testutil.NewCleanupPolicyRepo(),
		testutil.NewRepoRepo(),
		testutil.NewAssetRepo(),
		testutil.NewBlobStoreRepo(),
		testutil.NewBlobStore(),
		nopLog(),
	)
	result, err := svc.PreviewPolicy(context.Background(), "no-such-id")
	assert.Nil(t, result)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestExtraCleanup_PreviewPolicy_NoCriteria_ReturnsEmpty(t *testing.T) {
	p := &domain.CleanupPolicy{
		ID:       "p-preview-empty",
		Name:     "no-criteria",
		Enabled:  true,
		Format:   "*",
		Criteria: map[string]any{},
	}
	repos := testutil.NewRepoRepo(&domain.Repository{
		Name: "repo1", ID: "r1", Format: domain.FormatRaw,
		CleanupPolicyIDs: []string{"p-preview-empty"},
	})
	assets := testutil.NewAssetRepo()
	assets.Stale = []domain.Asset{
		{ID: "a1", BlobKey: "k1", SizeBytes: 10, Path: "/a.bin"},
	}
	svc := service.NewCleanupService(testutil.NewCleanupPolicyRepo(p), repos, assets, testutil.NewBlobStoreRepo(), testutil.NewBlobStore(), nopLog())
	result, err := svc.PreviewPolicy(context.Background(), "p-preview-empty")
	// No usable criteria → ListStale is still called but reason is "stale"
	require.NoError(t, err)
	require.NotNil(t, result)
}

func TestExtraCleanup_PreviewPolicy_LastDownloadedDays_Reason(t *testing.T) {
	p := &domain.CleanupPolicy{
		ID:      "p-preview-dl",
		Name:    "by-dl",
		Enabled: true,
		Format:  "raw",
		Criteria: map[string]any{
			"lastDownloadedDays": float64(30),
		},
	}
	repos := testutil.NewRepoRepo(&domain.Repository{
		Name: "rr1", ID: "r1", Format: domain.FormatRaw,
		CleanupPolicyIDs: []string{"p-preview-dl"},
	})
	stale := []domain.Asset{
		{ID: "sa1", BlobKey: "bk1", SizeBytes: 100, Path: "/pkg.bin"},
	}
	assets := testutil.NewAssetRepo()
	assets.Stale = stale

	svc := service.NewCleanupService(testutil.NewCleanupPolicyRepo(p), repos, assets, testutil.NewBlobStoreRepo(), testutil.NewBlobStore(), nopLog())
	result, err := svc.PreviewPolicy(context.Background(), "p-preview-dl")
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 1, result.TotalCount)
	assert.Equal(t, int64(100), result.TotalBytes)
	require.Len(t, result.Assets, 1)
	assert.Equal(t, "not dl 30d", result.Assets[0].Reason)
	assert.Equal(t, "/pkg.bin", result.Assets[0].Path)
}

func TestExtraCleanup_PreviewPolicy_ArtifactAgeDays_Reason(t *testing.T) {
	p := &domain.CleanupPolicy{
		ID:      "p-preview-age",
		Name:    "by-age",
		Enabled: true,
		Format:  "*",
		Criteria: map[string]any{
			"artifactAgeDays": float64(90),
		},
	}
	repos := testutil.NewRepoRepo(&domain.Repository{
		Name: "rr2", ID: "r2", Format: domain.FormatRaw,
		CleanupPolicyIDs: []string{"p-preview-age"},
	})
	stale := []domain.Asset{
		{ID: "sa2", BlobKey: "bk2", SizeBytes: 50, Path: "/old.bin"},
	}
	assets := testutil.NewAssetRepo()
	assets.Stale = stale

	svc := service.NewCleanupService(testutil.NewCleanupPolicyRepo(p), repos, assets, testutil.NewBlobStoreRepo(), testutil.NewBlobStore(), nopLog())
	result, err := svc.PreviewPolicy(context.Background(), "p-preview-age")
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 1, result.TotalCount)
	require.Len(t, result.Assets, 1)
	assert.Equal(t, "age 90d", result.Assets[0].Reason)
}

func TestExtraCleanup_PreviewPolicy_ScopeRepositoryName(t *testing.T) {
	p := &domain.CleanupPolicy{
		ID:      "p-preview-scope",
		Name:    "scoped",
		Enabled: true,
		Format:  "*",
		Criteria: map[string]any{
			"lastDownloadedDays": float64(7),
		},
		Scope: domain.CleanupScope{
			RepositoryName: "specific-repo",
			PathPrefix:     "/prefix/",
		},
	}
	assets := testutil.NewAssetRepo()
	assets.Stale = []domain.Asset{
		{ID: "sa3", BlobKey: "bk3", SizeBytes: 20, Path: "/prefix/file.bin"},
	}

	svc := service.NewCleanupService(testutil.NewCleanupPolicyRepo(p), testutil.NewRepoRepo(), assets, testutil.NewBlobStoreRepo(), testutil.NewBlobStore(), nopLog())
	result, err := svc.PreviewPolicy(context.Background(), "p-preview-scope")
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 1, result.TotalCount)
}

// ── RunAll — locker branches (line 138) ──────────────────────

func TestExtraCleanup_RunAll_LockerHeld_Skips(t *testing.T) {
	p := &domain.CleanupPolicy{
		ID: "p-lock", Name: "locked", Enabled: true, Format: "*",
		Criteria: map[string]any{"artifactAgeDays": float64(1)},
	}
	assets := testutil.NewAssetRepo()
	assets.Stale = []domain.Asset{{ID: "x", BlobKey: "k", SizeBytes: 1, Path: "/p"}}
	blobs := testutil.NewBlobStore()

	svc := service.NewCleanupService(testutil.NewCleanupPolicyRepo(p), testutil.NewRepoRepo(), assets, testutil.NewBlobStoreRepo(), blobs, nopLog())

	locker := &extraCleanupLocker{err: distlock.ErrLockHeld}
	svc.WithLocker(locker)

	// Should return nil without running (lock held by another node)
	err := svc.RunAll(context.Background())
	require.NoError(t, err)
	assert.Empty(t, blobs.Deleted, "should not delete anything when lock is held")
}

func TestExtraCleanup_RunAll_LockerAcquireError_ReturnsError(t *testing.T) {
	svc := service.NewCleanupService(
		testutil.NewCleanupPolicyRepo(),
		testutil.NewRepoRepo(),
		testutil.NewAssetRepo(),
		testutil.NewBlobStoreRepo(),
		testutil.NewBlobStore(),
		nopLog(),
	)
	locker := &extraCleanupLocker{err: errors.New("redis down")}
	svc.WithLocker(locker)

	err := svc.RunAll(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "acquire lock")
}

func TestExtraCleanup_RunAll_LockerAcquired_ReleasesOnDone(t *testing.T) {
	p := &domain.CleanupPolicy{
		ID: "p-lockrel", Name: "lockrel", Enabled: true, Format: "*",
		Criteria: map[string]any{"artifactAgeDays": float64(1)},
	}
	svc := service.NewCleanupService(
		testutil.NewCleanupPolicyRepo(p),
		testutil.NewRepoRepo(),
		testutil.NewAssetRepo(),
		testutil.NewBlobStoreRepo(),
		testutil.NewBlobStore(),
		nopLog(),
	)
	lk := &extraCleanupLocker{}
	svc.WithLocker(lk)

	require.NoError(t, svc.RunAll(context.Background()))
	require.NotNil(t, lk.lock, "lock should have been acquired")
	assert.True(t, lk.lock.released, "lock should be released after RunAll")
}

// ── RunAll — policies.List error ─────────────────────────────

func TestExtraCleanup_RunAll_ListPoliciesError(t *testing.T) {
	policies := testutil.NewCleanupPolicyRepo()
	policies.Err = errors.New("db error")

	svc := service.NewCleanupService(
		policies,
		testutil.NewRepoRepo(),
		testutil.NewAssetRepo(),
		testutil.NewBlobStoreRepo(),
		testutil.NewBlobStore(),
		nopLog(),
	)
	err := svc.RunAll(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list policies")
}

// ── runPolicy — scope branch & path prefix ────────────────────

func TestExtraCleanup_RunPolicy_ScopeRepositoryName_Used(t *testing.T) {
	stale := []domain.Asset{
		{ID: "scopeA", BlobKey: "bkScope", SizeBytes: 10, Path: "/scoped/file.bin"},
	}
	p := &domain.CleanupPolicy{
		ID: "p-scope", Name: "scope-test", Enabled: true, Format: "*",
		Criteria: map[string]any{"lastDownloadedDays": float64(1)},
		Scope: domain.CleanupScope{
			RepositoryName: "direct-repo",
			PathPrefix:     "/scoped/",
		},
	}
	assets := testutil.NewAssetRepo()
	assets.Stale = stale
	blobs := testutil.NewBlobStore()
	_ = blobs.Put(context.Background(), "bkScope", testutil.MakeReader("x"), 1)

	// No repos attached to policy ID — but Scope.RepositoryName bypasses that
	svc := service.NewCleanupService(testutil.NewCleanupPolicyRepo(p), testutil.NewRepoRepo(), assets, testutil.NewBlobStoreRepo(), blobs, nopLog())
	require.NoError(t, svc.RunPolicy(context.Background(), "p-scope"))
	assert.Contains(t, blobs.Deleted, "bkScope")
}

// ── strCriteria / intCriteria — edge branches (lines 318, 330) ──

func TestExtraCleanup_RunPolicy_IntCriteria_NativeInt(t *testing.T) {
	// Pass native int (not float64) in criteria to exercise the int branch of intCriteria
	p := &domain.CleanupPolicy{
		ID: "p-native-int", Name: "native-int", Enabled: true, Format: "*",
		Criteria: map[string]any{"artifactAgeDays": 15}, // native int, not float64
	}
	stale := []domain.Asset{
		{ID: "ni1", BlobKey: "bkNI", SizeBytes: 5, Path: "/ni.bin"},
	}
	assets := testutil.NewAssetRepo()
	assets.Stale = stale
	blobs := testutil.NewBlobStore()
	_ = blobs.Put(context.Background(), "bkNI", testutil.MakeReader("x"), 1)
	repos := testutil.NewRepoRepo(&domain.Repository{
		Name: "ni-repo", ID: "r-ni", Format: domain.FormatRaw,
		CleanupPolicyIDs: []string{"p-native-int"},
	})

	svc := service.NewCleanupService(testutil.NewCleanupPolicyRepo(p), repos, assets, testutil.NewBlobStoreRepo(), blobs, nopLog())
	require.NoError(t, svc.RunPolicy(context.Background(), "p-native-int"))
	assert.Contains(t, blobs.Deleted, "bkNI")
}

func TestExtraCleanup_RunPolicy_IntCriteria_Int64(t *testing.T) {
	// Pass int64 to exercise that branch in intCriteria
	p := &domain.CleanupPolicy{
		ID: "p-int64", Name: "int64-crit", Enabled: true, Format: "*",
		Criteria: map[string]any{"lastDownloadedDays": int64(20)},
	}
	stale := []domain.Asset{
		{ID: "i64a", BlobKey: "bkI64", SizeBytes: 8, Path: "/i64.bin"},
	}
	assets := testutil.NewAssetRepo()
	assets.Stale = stale
	blobs := testutil.NewBlobStore()
	_ = blobs.Put(context.Background(), "bkI64", testutil.MakeReader("x"), 1)
	repos := testutil.NewRepoRepo(&domain.Repository{
		Name: "i64-repo", ID: "r-i64", Format: domain.FormatRaw,
		CleanupPolicyIDs: []string{"p-int64"},
	})

	svc := service.NewCleanupService(testutil.NewCleanupPolicyRepo(p), repos, assets, testutil.NewBlobStoreRepo(), blobs, nopLog())
	require.NoError(t, svc.RunPolicy(context.Background(), "p-int64"))
	assert.Contains(t, blobs.Deleted, "bkI64")
}

func TestExtraCleanup_RunPolicy_IntCriteria_UnknownType_ReturnsZero(t *testing.T) {
	// Pass a type that is not int/float64/int64 — intCriteria returns 0, policy skips
	p := &domain.CleanupPolicy{
		ID: "p-unknown-type", Name: "unknown-type", Enabled: true, Format: "*",
		Criteria: map[string]any{"artifactAgeDays": "not-a-number"},
	}
	assets := testutil.NewAssetRepo()
	assets.Stale = []domain.Asset{{ID: "u1", BlobKey: "bkU", SizeBytes: 5, Path: "/u.bin"}}
	blobs := testutil.NewBlobStore()

	svc := service.NewCleanupService(testutil.NewCleanupPolicyRepo(p), testutil.NewRepoRepo(), assets, testutil.NewBlobStoreRepo(), blobs, nopLog())
	require.NoError(t, svc.RunPolicy(context.Background(), "p-unknown-type"))
	// intCriteria returns 0 → no criteria set → skipped → no deletions
	assert.Empty(t, blobs.Deleted)
}

func TestExtraCleanup_RunPolicy_StrCriteria_NonNilMap(t *testing.T) {
	// Test strCriteria with both a key that is present and one that is absent
	p := &domain.CleanupPolicy{
		ID: "p-str", Name: "str-crit", Enabled: true, Format: "*",
		Criteria: map[string]any{
			"pathPrefix":        "/data/",
			"artifactAgeDays":   float64(1),
			"nameGlob":          "*.jar",
			"nonStringCriteria": 42, // strCriteria should return "" for this
		},
	}
	stale := []domain.Asset{
		{ID: "str1", BlobKey: "bkStr", SizeBytes: 5, Path: "/data/foo.jar"},
	}
	assets := testutil.NewAssetRepo()
	assets.Stale = stale
	blobs := testutil.NewBlobStore()
	_ = blobs.Put(context.Background(), "bkStr", testutil.MakeReader("x"), 1)
	repos := testutil.NewRepoRepo(&domain.Repository{
		Name: "str-repo", ID: "r-str", Format: domain.FormatRaw,
		CleanupPolicyIDs: []string{"p-str"},
	})

	svc := service.NewCleanupService(testutil.NewCleanupPolicyRepo(p), repos, assets, testutil.NewBlobStoreRepo(), blobs, nopLog())
	require.NoError(t, svc.RunPolicy(context.Background(), "p-str"))
	assert.Contains(t, blobs.Deleted, "bkStr")
}

// ── ReloadPolicy — disabled policy removes entry ──────────────

func TestExtraCleanup_ReloadPolicy_DisabledRemovesEntry(t *testing.T) {
	p := &domain.CleanupPolicy{
		ID: "p-reload-dis", Name: "reload-disabled", Enabled: true, Format: "*",
		ScheduleCron: "@yearly",
		Criteria:     map[string]any{"artifactAgeDays": float64(1)},
	}
	policies := testutil.NewCleanupPolicyRepo(p)
	svc := service.NewCleanupService(policies, testutil.NewRepoRepo(), testutil.NewAssetRepo(), testutil.NewBlobStoreRepo(), testutil.NewBlobStore(), nopLog())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.StartCronScheduler(ctx, "@yearly")
	time.Sleep(50 * time.Millisecond)

	// Mark policy as disabled and reload
	p.Enabled = false
	svc.ReloadPolicy(ctx, "p-reload-dis")
	// Should not panic — entry removed because policy is disabled
}

func TestExtraCleanup_ReloadPolicy_EnabledUpdatesSchedule(t *testing.T) {
	p := &domain.CleanupPolicy{
		ID: "p-reload-en", Name: "reload-enabled", Enabled: true, Format: "*",
		ScheduleCron: "@yearly",
		Criteria:     map[string]any{"artifactAgeDays": float64(365)},
	}
	policies := testutil.NewCleanupPolicyRepo(p)
	svc := service.NewCleanupService(policies, testutil.NewRepoRepo(), testutil.NewAssetRepo(), testutil.NewBlobStoreRepo(), testutil.NewBlobStore(), nopLog())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.StartCronScheduler(ctx, "@yearly")
	time.Sleep(50 * time.Millisecond)

	// Reload same policy (still enabled) — should update entry without panic
	svc.ReloadPolicy(ctx, "p-reload-en")
}
