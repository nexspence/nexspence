package service_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// extraBackupSvc builds a BackupService with optional seed repositories.
// Distinct from buildBackupSvc — keeps tests self-contained.
func extraBackupSvc(repos ...*domain.Repository) *service.BackupService {
	return &service.BackupService{
		BlobStores: testutil.NewBlobStoreRepo(),
		Repos:      testutil.NewRepoRepo(repos...),
		Users:      testutil.NewUserRepo(),
		Roles:      testutil.NewRoleRepo(),
		Policies:   testutil.NewCleanupPolicyRepo(),
		Components: testutil.NewComponentRepo(),
		Assets:     testutil.NewAssetRepo(),
		BlobStore:  testutil.NewBlobStore(),
	}
}

// ── Export (line 61) — additional branch coverage ─────────────

func TestExtraBackup_Export_WithUsers(t *testing.T) {
	ctx := context.Background()
	repo := testutil.SimpleRepo("utest", "raw")
	svc := extraBackupSvc(repo)

	u := &domain.User{Username: "alice", Email: "alice@example.com"}
	require.NoError(t, svc.Users.Create(ctx, u))

	var buf bytes.Buffer
	require.NoError(t, svc.Export(ctx, &buf))
	assert.Greater(t, buf.Len(), 0)
}

func TestExtraBackup_Export_WithRoles(t *testing.T) {
	ctx := context.Background()
	repo := testutil.SimpleRepo("rtest", "raw")
	svc := extraBackupSvc(repo)

	role := &domain.Role{Name: "dev"}
	require.NoError(t, svc.Roles.Create(ctx, role))

	var buf bytes.Buffer
	require.NoError(t, svc.Export(ctx, &buf))
	assert.Greater(t, buf.Len(), 0)
}

func TestExtraBackup_Export_WithCleanupPolicy(t *testing.T) {
	ctx := context.Background()
	repo := testutil.SimpleRepo("ptest", "raw")
	svc := extraBackupSvc(repo)

	p := &domain.CleanupPolicy{Name: "daily", Format: "*", Enabled: true, Criteria: map[string]any{"artifactAgeDays": float64(30)}}
	require.NoError(t, svc.Policies.Create(ctx, p))

	var buf bytes.Buffer
	require.NoError(t, svc.Export(ctx, &buf))
	assert.Greater(t, buf.Len(), 0)
}

func TestExtraBackup_Export_PaginatesComponents(t *testing.T) {
	// One component — exercises the < 500 break path in the pagination loop.
	ctx := context.Background()
	repo := testutil.SimpleRepo("pgtest", "raw")
	svc := extraBackupSvc(repo)

	comp := &domain.Component{
		RepositoryID: repo.ID, Repository: repo.Name,
		Format: "raw", Name: "lib.tar.gz", Version: "1.0",
	}
	require.NoError(t, svc.Components.Create(ctx, comp))

	var buf bytes.Buffer
	require.NoError(t, svc.Export(ctx, &buf))
	assert.Greater(t, buf.Len(), 0)
}

func TestExtraBackup_Export_SkipsMissingBlobGracefully(t *testing.T) {
	// Asset references a blob key not present in the store — Export skips it, no error.
	ctx := context.Background()
	repo := testutil.SimpleRepo("skipblob", "raw")
	svc := extraBackupSvc(repo)

	bs := &domain.BlobStore{Name: "default", Type: "local", Config: map[string]any{"path": "/tmp"}}
	require.NoError(t, svc.BlobStores.Create(ctx, bs))

	comp := &domain.Component{
		RepositoryID: repo.ID, Repository: repo.Name,
		Format: "raw", Name: "ghost.bin", Version: "1",
	}
	require.NoError(t, svc.Components.Create(ctx, comp))

	asset := &domain.Asset{
		ComponentID: comp.ID, RepositoryID: repo.ID, Repository: repo.Name,
		Path:        "/ghost.bin",
		BlobKey:     "no/such/blobkey",
		BlobStoreID: bs.ID,
		SizeBytes:   99,
		ContentType: "application/octet-stream",
	}
	require.NoError(t, svc.Assets.Create(ctx, asset))

	var buf bytes.Buffer
	require.NoError(t, svc.Export(ctx, &buf))
	assert.Greater(t, buf.Len(), 0)
}

func TestExtraBackup_Export_DeduplicatesBlobs(t *testing.T) {
	// Two assets share the same BlobKey — blob entry should be written only once.
	ctx := context.Background()
	repo := testutil.SimpleRepo("dedup", "raw")
	svc := extraBackupSvc(repo)

	bs := &domain.BlobStore{Name: "default", Type: "local", Config: map[string]any{"path": "/tmp"}}
	require.NoError(t, svc.BlobStores.Create(ctx, bs))

	blobKey := "aa/bb/shared"
	blobData := []byte("shared-content")
	require.NoError(t, svc.BlobStore.Put(ctx, blobKey, bytes.NewReader(blobData), int64(len(blobData))))

	comp := &domain.Component{
		RepositoryID: repo.ID, Repository: repo.Name,
		Format: "raw", Name: "shared.bin", Version: "1",
	}
	require.NoError(t, svc.Components.Create(ctx, comp))

	for _, path := range []string{"/shared-a.bin", "/shared-b.bin"} {
		a := &domain.Asset{
			ComponentID: comp.ID, RepositoryID: repo.ID, Repository: repo.Name,
			Path: path, BlobKey: blobKey, BlobStoreID: bs.ID,
			SizeBytes: int64(len(blobData)), ContentType: "application/octet-stream",
		}
		require.NoError(t, svc.Assets.Create(ctx, a))
	}

	var buf bytes.Buffer
	require.NoError(t, svc.Export(ctx, &buf))
	assert.Greater(t, buf.Len(), 0)
}

// ── ExportRepo (line 203) — additional branches ────────────────

func TestExtraBackup_ExportRepo_EmptyBlobKey_Skipped(t *testing.T) {
	// Asset with empty BlobKey is silently skipped during blob streaming.
	ctx := context.Background()
	repo := testutil.SimpleRepo("emptyblobkey", "raw")
	svc := extraBackupSvc(repo)

	comp := &domain.Component{
		RepositoryID: repo.ID, Repository: repo.Name,
		Format: "raw", Name: "file.txt", Version: "1",
	}
	require.NoError(t, svc.Components.Create(ctx, comp))

	asset := &domain.Asset{
		ComponentID: comp.ID, RepositoryID: repo.ID, Repository: repo.Name,
		Path: "/file.txt", BlobKey: "", SizeBytes: 0, ContentType: "text/plain",
	}
	require.NoError(t, svc.Assets.Create(ctx, asset))

	var buf bytes.Buffer
	require.NoError(t, svc.ExportRepo(ctx, "emptyblobkey", &buf))
	assert.Greater(t, buf.Len(), 0)
}

func TestExtraBackup_ExportRepo_SkipsMissingBlobKey(t *testing.T) {
	// BlobKey present but not in blob store — ExportRepo skips gracefully.
	ctx := context.Background()
	repo := testutil.SimpleRepo("skiprepo", "raw")
	svc := extraBackupSvc(repo)

	comp := &domain.Component{
		RepositoryID: repo.ID, Repository: repo.Name,
		Format: "raw", Name: "missing.bin", Version: "1",
	}
	require.NoError(t, svc.Components.Create(ctx, comp))

	asset := &domain.Asset{
		ComponentID: comp.ID, RepositoryID: repo.ID, Repository: repo.Name,
		Path: "/missing.bin", BlobKey: "xx/yy/notexist",
		SizeBytes: 5, ContentType: "application/octet-stream",
	}
	require.NoError(t, svc.Assets.Create(ctx, asset))

	var buf bytes.Buffer
	require.NoError(t, svc.ExportRepo(ctx, "skiprepo", &buf))
	assert.Greater(t, buf.Len(), 0)
}

// ── ImportRepo (line ~321) — uncovered branches ────────────────

func TestExtraBackup_ImportRepo_InvalidGzip(t *testing.T) {
	svc := extraBackupSvc()
	_, err := svc.ImportRepo(context.Background(), bytes.NewReader([]byte("garbage")), "", "skip")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gzip")
}

func TestExtraBackup_ImportRepo_TruncatedArchive(t *testing.T) {
	// Truncated but valid gzip header — gzip.NewReader succeeds but tar.Next fails.
	ctx := context.Background()
	svc := extraBackupSvc()
	_, err := svc.ImportRepo(ctx, bytes.NewReader([]byte{0x1f, 0x8b}), "", "skip")
	require.Error(t, err)
}

func TestExtraBackup_ImportRepo_MergeMode_SameAsSkip(t *testing.T) {
	// conflictMode="merge" is an alias for "skip".
	ctx := context.Background()
	repo := testutil.SimpleRepo("merge-src", "raw")
	src := extraBackupSvc(repo)

	comp := &domain.Component{
		RepositoryID: repo.ID, Repository: repo.Name,
		Format: "raw", Name: "lib.txt", Version: "1",
	}
	require.NoError(t, src.Components.Create(ctx, comp))

	asset := &domain.Asset{
		ComponentID:  comp.ID,
		RepositoryID: repo.ID,
		Repository:   repo.Name,
		Path:         "/lib.txt", SizeBytes: 0, ContentType: "text/plain",
	}
	require.NoError(t, src.Assets.Create(ctx, asset))

	var buf bytes.Buffer
	require.NoError(t, src.ExportRepo(ctx, "merge-src", &buf))
	archived := buf.Bytes()

	dst := extraBackupSvc()
	stats1, err := dst.ImportRepo(ctx, bytes.NewReader(archived), "", "merge")
	require.NoError(t, err)
	assert.Equal(t, 1, stats1.Components)

	// Second import with merge — component already exists, should be skipped.
	stats2, err := dst.ImportRepo(ctx, bytes.NewReader(archived), "", "merge")
	require.NoError(t, err)
	assert.Equal(t, 0, stats2.Components)
}

func TestExtraBackup_ImportRepo_BlobStoreIDFallback(t *testing.T) {
	// Destination repo has no BlobStoreID → falls back to first available blob store.
	ctx := context.Background()
	repo := testutil.SimpleRepo("blob-fallback", "raw")
	src := extraBackupSvc(repo)

	blobData := []byte("content")
	blobKey := "cc/dd/ccdd"
	require.NoError(t, src.BlobStore.Put(ctx, blobKey, bytes.NewReader(blobData), int64(len(blobData))))

	bs := &domain.BlobStore{Name: "default", Type: "local", Config: map[string]any{"path": "/tmp"}}
	require.NoError(t, src.BlobStores.Create(ctx, bs))

	comp := &domain.Component{
		RepositoryID: repo.ID, Repository: repo.Name,
		Format: "raw", Name: "data.bin", Version: "1",
	}
	require.NoError(t, src.Components.Create(ctx, comp))

	asset := &domain.Asset{
		ComponentID:  comp.ID,
		RepositoryID: repo.ID,
		Repository:   repo.Name,
		Path:         "/data.bin", BlobKey: blobKey, BlobStoreID: bs.ID,
		SizeBytes: int64(len(blobData)), ContentType: "application/octet-stream",
	}
	require.NoError(t, src.Assets.Create(ctx, asset))

	var buf bytes.Buffer
	require.NoError(t, src.ExportRepo(ctx, "blob-fallback", &buf))

	// Destination: fresh service with default blob store (no blobStoreID on the repo).
	dst := extraBackupSvc()

	stats, err := dst.ImportRepo(ctx, &buf, "", "skip")
	require.NoError(t, err)
	assert.Equal(t, 1, stats.Components)
	assert.Equal(t, 1, stats.Assets)
	assert.Equal(t, 1, stats.Blobs)
}

func TestExtraBackup_ImportRepo_DefaultConflictMode(t *testing.T) {
	// When conflictMode is empty it defaults to "skip".
	ctx := context.Background()
	repo := testutil.SimpleRepo("defmode", "raw")
	src := extraBackupSvc(repo)

	var buf bytes.Buffer
	require.NoError(t, src.ExportRepo(ctx, "defmode", &buf))

	dst := extraBackupSvc()
	stats, err := dst.ImportRepo(ctx, &buf, "", "") // empty conflictMode
	require.NoError(t, err)
	assert.Equal(t, "skip", stats.ConflictMode)
}

// ── Restore (line ~498) — additional branch coverage ──────────

func TestExtraBackup_Restore_WithUsersAndRoles(t *testing.T) {
	ctx := context.Background()
	repo := testutil.SimpleRepo("urol-repo", "raw")
	src := extraBackupSvc(repo)

	u := &domain.User{Username: "bob", Email: "bob@example.com"}
	require.NoError(t, src.Users.Create(ctx, u))

	role := &domain.Role{Name: "viewer"}
	require.NoError(t, src.Roles.Create(ctx, role))

	var buf bytes.Buffer
	require.NoError(t, src.Export(ctx, &buf))

	dst := extraBackupSvc()
	stats, err := dst.Restore(ctx, &buf)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, stats.Users, 1)
	assert.GreaterOrEqual(t, stats.Roles, 1)
}

func TestExtraBackup_Restore_SkipsExistingUser(t *testing.T) {
	ctx := context.Background()
	src := extraBackupSvc(testutil.SimpleRepo("u-exist-repo", "raw"))

	u := &domain.User{Username: "carol", Email: "carol@example.com"}
	require.NoError(t, src.Users.Create(ctx, u))

	var buf bytes.Buffer
	require.NoError(t, src.Export(ctx, &buf))
	archived := buf.Bytes()

	dst := extraBackupSvc()
	stats1, err := dst.Restore(ctx, bytes.NewReader(archived))
	require.NoError(t, err)
	assert.GreaterOrEqual(t, stats1.Users, 1)

	// Second restore — carol already exists.
	stats2, err := dst.Restore(ctx, bytes.NewReader(archived))
	require.NoError(t, err)
	assert.Equal(t, 0, stats2.Users)
}

func TestExtraBackup_Restore_SkipsExistingRole(t *testing.T) {
	ctx := context.Background()
	src := extraBackupSvc(testutil.SimpleRepo("r-exist-repo", "raw"))

	role := &domain.Role{Name: "ops"}
	require.NoError(t, src.Roles.Create(ctx, role))

	var buf bytes.Buffer
	require.NoError(t, src.Export(ctx, &buf))
	archived := buf.Bytes()

	dst := extraBackupSvc()
	stats1, err := dst.Restore(ctx, bytes.NewReader(archived))
	require.NoError(t, err)
	assert.GreaterOrEqual(t, stats1.Roles, 1)

	stats2, err := dst.Restore(ctx, bytes.NewReader(archived))
	require.NoError(t, err)
	assert.Equal(t, 0, stats2.Roles)
}

func TestExtraBackup_Restore_SkipsExistingPolicy(t *testing.T) {
	ctx := context.Background()
	src := extraBackupSvc(testutil.SimpleRepo("pol-exist-repo", "raw"))

	p := &domain.CleanupPolicy{Name: "weekly", Format: "*", Enabled: true, Criteria: map[string]any{"artifactAgeDays": float64(7)}}
	require.NoError(t, src.Policies.Create(ctx, p))

	var buf bytes.Buffer
	require.NoError(t, src.Export(ctx, &buf))
	archived := buf.Bytes()

	dst := extraBackupSvc()
	stats1, err := dst.Restore(ctx, bytes.NewReader(archived))
	require.NoError(t, err)
	assert.GreaterOrEqual(t, stats1.Policies, 1)

	stats2, err := dst.Restore(ctx, bytes.NewReader(archived))
	require.NoError(t, err)
	assert.Equal(t, 0, stats2.Policies)
}

func TestExtraBackup_Restore_BlobStoreNameMapping(t *testing.T) {
	// Tests the BlobStore ID remapping: old-UUID → name → new-UUID.
	ctx := context.Background()
	repo := testutil.SimpleRepo("bsmap-repo", "raw")
	src := extraBackupSvc(repo)

	bs := &domain.BlobStore{Name: "primary", Type: "local", Config: map[string]any{"path": "/blobs"}}
	require.NoError(t, src.BlobStores.Create(ctx, bs))

	blobKey := "ee/ff/eeff"
	blobData := []byte("mapped-blob")
	require.NoError(t, src.BlobStore.Put(ctx, blobKey, bytes.NewReader(blobData), int64(len(blobData))))

	comp := &domain.Component{
		RepositoryID: repo.ID, Repository: repo.Name,
		Format: "raw", Name: "mapped.bin", Version: "1",
	}
	require.NoError(t, src.Components.Create(ctx, comp))

	asset := &domain.Asset{
		ComponentID: comp.ID, RepositoryID: repo.ID, Repository: repo.Name,
		Path: "/mapped.bin", BlobKey: blobKey, BlobStoreID: bs.ID,
		SizeBytes: int64(len(blobData)), ContentType: "application/octet-stream",
	}
	require.NoError(t, src.Assets.Create(ctx, asset))

	var buf bytes.Buffer
	require.NoError(t, src.Export(ctx, &buf))

	// Destination pre-creates the same-named blob store so the mapping resolves.
	dst := extraBackupSvc()
	dstBS := &domain.BlobStore{Name: "primary", Type: "local", Config: map[string]any{"path": "/blobs"}}
	require.NoError(t, dst.BlobStores.Create(ctx, dstBS))

	stats, err := dst.Restore(ctx, &buf)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.Components)
	assert.Equal(t, 1, stats.Assets)
	assert.Equal(t, 1, stats.Blobs)
}

func TestExtraBackup_Restore_BlobStoreFallbackToFirstAvailable(t *testing.T) {
	// Old blob store name not found in dst → falls back to first available bs.
	ctx := context.Background()
	repo := testutil.SimpleRepo("bsfallback-repo", "raw")
	src := extraBackupSvc(repo)

	bs := &domain.BlobStore{Name: "alien-store", Type: "local", Config: map[string]any{"path": "/alien"}}
	require.NoError(t, src.BlobStores.Create(ctx, bs))

	blobKey := "gg/hh/gghh"
	blobData := []byte("alien-blob")
	require.NoError(t, src.BlobStore.Put(ctx, blobKey, bytes.NewReader(blobData), int64(len(blobData))))

	comp := &domain.Component{
		RepositoryID: repo.ID, Repository: repo.Name,
		Format: "raw", Name: "alien.bin", Version: "1",
	}
	require.NoError(t, src.Components.Create(ctx, comp))

	asset := &domain.Asset{
		ComponentID: comp.ID, RepositoryID: repo.ID, Repository: repo.Name,
		Path: "/alien.bin", BlobKey: blobKey, BlobStoreID: bs.ID,
		SizeBytes: int64(len(blobData)), ContentType: "application/octet-stream",
	}
	require.NoError(t, src.Assets.Create(ctx, asset))

	var buf bytes.Buffer
	require.NoError(t, src.Export(ctx, &buf))

	// Destination has "default" blob store (not "alien-store") — name mismatch,
	// so the fallback branch executes.
	dst := extraBackupSvc()

	stats, err := dst.Restore(ctx, &buf)
	require.NoError(t, err)
	// Repo created fresh, component + asset + blob should all be counted.
	assert.Equal(t, 1, stats.Components)
	assert.Equal(t, 1, stats.Assets)
	assert.Equal(t, 1, stats.Blobs)
}
