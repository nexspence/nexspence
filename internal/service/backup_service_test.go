package service_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

func buildBackupSvc(repos ...*domain.Repository) *service.BackupService {
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

func TestBackup_ExportEmptyRepo(t *testing.T) {
	repo := testutil.SimpleRepo("myrepo", "raw")
	svc := buildBackupSvc(repo)

	var buf bytes.Buffer
	err := svc.Export(context.Background(), &buf)
	require.NoError(t, err)
	assert.Greater(t, buf.Len(), 0, "export should produce non-empty archive")
}

func TestBackup_ExportRestoreRoundtrip(t *testing.T) {
	ctx := context.Background()
	repo := testutil.SimpleRepo("rawrepo", "raw")

	// ── Source system ──────────────────────────────────────────
	src := buildBackupSvc(repo)

	// Add a blob store entry.
	bs := &domain.BlobStore{Name: "default", Type: "local", Config: map[string]any{"path": "/tmp/blobs"}}
	require.NoError(t, src.BlobStores.Create(ctx, bs))

	// Store an artifact through the blob store so an asset exists.
	blobKey := "ab/cd/abcdef1234"
	blobContent := []byte("hello-artifact")
	require.NoError(t, src.BlobStore.Put(ctx, blobKey, bytes.NewReader(blobContent), int64(len(blobContent))))

	// Create component + asset manually (simulates what StoreArtifact does).
	comp := &domain.Component{
		RepositoryID: repo.ID, Repository: repo.Name,
		Format: "raw", Name: "myfile.txt", Version: "1",
	}
	require.NoError(t, src.Components.Create(ctx, comp))

	asset := &domain.Asset{
		ComponentID: comp.ID, RepositoryID: repo.ID, Repository: repo.Name,
		Path: "/myfile.txt", BlobKey: blobKey, BlobStoreID: bs.ID,
		SizeBytes: int64(len(blobContent)), ContentType: "text/plain",
	}
	require.NoError(t, src.Assets.Create(ctx, asset))

	// Export.
	var buf bytes.Buffer
	require.NoError(t, src.Export(ctx, &buf))

	// ── Destination system (fresh) ─────────────────────────────
	repo2 := testutil.SimpleRepo("rawrepo", "raw")
	dst := buildBackupSvc(repo2)

	stats, err := dst.Restore(ctx, &buf)
	require.NoError(t, err)

	// The repo already existed in dst (seeded by testutil), so Repos may be 0.
	// The important parts are component, asset and blob restore.
	assert.Equal(t, 1, stats.Components)
	assert.Equal(t, 1, stats.Assets)
	assert.Equal(t, 1, stats.Blobs)

	// Blob bytes should be present in the destination blob store.
	rc, size, err := dst.BlobStore.Get(ctx, blobKey)
	require.NoError(t, err)
	defer rc.Close()
	assert.Equal(t, int64(len(blobContent)), size)
}

func TestBackup_RestoreSkipsExistingRecords(t *testing.T) {
	ctx := context.Background()
	repo := testutil.SimpleRepo("repo1", "raw")
	src := buildBackupSvc(repo)

	var buf bytes.Buffer
	require.NoError(t, src.Export(ctx, &buf))

	// First restore.
	dst := buildBackupSvc(testutil.SimpleRepo("repo1", "raw"))
	stats1, err := dst.Restore(ctx, bytes.NewReader(buf.Bytes()))
	require.NoError(t, err)

	// Second restore into same destination — all records already exist.
	stats2, err := dst.Restore(ctx, bytes.NewReader(buf.Bytes()))
	require.NoError(t, err)

	// Second restore should report 0 new imports since everything was already there.
	assert.LessOrEqual(t, stats2.Repos, stats1.Repos)
	assert.Equal(t, 0, stats2.Components)
	assert.Equal(t, 0, stats2.Assets)
}

func TestBackup_InvalidArchive(t *testing.T) {
	ctx := context.Background()
	repo := testutil.SimpleRepo("r", "raw")
	svc := buildBackupSvc(repo)

	_, err := svc.Restore(ctx, bytes.NewReader([]byte("not a gzip")))
	assert.Error(t, err, "should reject non-gzip input")
}

func TestBackupRepo_ExportRoundtrip(t *testing.T) {
	ctx := context.Background()
	repo := testutil.SimpleRepo("myrepo", "raw")
	svc := buildBackupSvc(repo)

	// Add component + asset + blob.
	comp := &domain.Component{
		RepositoryID: repo.ID, Repository: repo.Name,
		Format: "raw", Name: "pkg.tar.gz", Version: "1.0",
	}
	require.NoError(t, svc.Components.Create(ctx, comp))

	blobKey := "ab/cd/abcdef"
	blobData := []byte("artifact-bytes")
	require.NoError(t, svc.BlobStore.Put(ctx, blobKey, bytes.NewReader(blobData), int64(len(blobData))))

	bs := &domain.BlobStore{Name: "default", Type: "local", Config: map[string]any{"path": "/tmp"}}
	require.NoError(t, svc.BlobStores.Create(ctx, bs))

	asset := &domain.Asset{
		ComponentID: comp.ID, RepositoryID: repo.ID, Repository: repo.Name,
		Path: "/pkg.tar.gz", BlobKey: blobKey, BlobStoreID: bs.ID,
		SizeBytes: int64(len(blobData)), ContentType: "application/gzip",
	}
	require.NoError(t, svc.Assets.Create(ctx, asset))

	var buf bytes.Buffer
	require.NoError(t, svc.ExportRepo(ctx, "myrepo", &buf))
	assert.Greater(t, buf.Len(), 0)

	// Archive must contain manifest, repository, components, assets, blobs entries.
	gr, err := gzip.NewReader(&buf)
	require.NoError(t, err)
	tr := tar.NewReader(gr)
	entries := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		entries[hdr.Name] = true
	}
	assert.True(t, entries["manifest.json"])
	assert.True(t, entries["repository.json"])
	assert.True(t, entries["components.json"])
	assert.True(t, entries["assets.json"])
	assert.True(t, entries["blobs/"+blobKey])
}

func TestBackupRepo_ExportNotFound(t *testing.T) {
	svc := buildBackupSvc(testutil.SimpleRepo("exists", "raw"))
	var buf bytes.Buffer
	err := svc.ExportRepo(context.Background(), "no-such-repo", &buf)
	assert.ErrorIs(t, err, service.ErrRepoNotFound)
}

func TestBackupRepo_ImportRoundtrip(t *testing.T) {
	ctx := context.Background()
	srcRepo := testutil.SimpleRepo("srcrepo", "raw")
	src := buildBackupSvc(srcRepo)

	bs := &domain.BlobStore{Name: "default", Type: "local", Config: map[string]any{"path": "/tmp"}}
	require.NoError(t, src.BlobStores.Create(ctx, bs))

	comp := &domain.Component{
		RepositoryID: srcRepo.ID, Repository: "srcrepo",
		Format: "raw", Name: "file.txt", Version: "1",
	}
	require.NoError(t, src.Components.Create(ctx, comp))

	blobData := []byte("hello")
	blobKey := "aa/bb/aabb"
	require.NoError(t, src.BlobStore.Put(ctx, blobKey, bytes.NewReader(blobData), int64(len(blobData))))

	asset := &domain.Asset{
		ComponentID: comp.ID, RepositoryID: srcRepo.ID, Repository: "srcrepo",
		Path: "/file.txt", BlobKey: blobKey, BlobStoreID: bs.ID,
		SizeBytes: int64(len(blobData)), ContentType: "text/plain",
	}
	require.NoError(t, src.Assets.Create(ctx, asset))

	var buf bytes.Buffer
	require.NoError(t, src.ExportRepo(ctx, "srcrepo", &buf))

	dst := buildBackupSvc()
	require.NoError(t, dst.BlobStores.Create(ctx, &domain.BlobStore{Name: "default", Type: "local", Config: map[string]any{"path": "/tmp"}}))

	stats, err := dst.ImportRepo(ctx, &buf, "", "skip")
	require.NoError(t, err)
	assert.Equal(t, "srcrepo", stats.Repository)
	assert.Equal(t, 1, stats.Components)
	assert.Equal(t, 1, stats.Assets)
	assert.Equal(t, 1, stats.Blobs)

	// Blob should be present in destination.
	rc, _, err := dst.BlobStore.Get(ctx, blobKey)
	require.NoError(t, err)
	rc.Close()
}

func TestBackupRepo_ImportSkipIdempotent(t *testing.T) {
	ctx := context.Background()
	repo := testutil.SimpleRepo("repo1", "raw")
	src := buildBackupSvc(repo)

	comp := &domain.Component{RepositoryID: repo.ID, Repository: "repo1", Format: "raw", Name: "f.txt", Version: "1"}
	require.NoError(t, src.Components.Create(ctx, comp))
	asset := &domain.Asset{ComponentID: comp.ID, RepositoryID: repo.ID, Repository: "repo1", Path: "/f.txt", SizeBytes: 0, ContentType: "text/plain"}
	require.NoError(t, src.Assets.Create(ctx, asset))

	var buf bytes.Buffer
	require.NoError(t, src.ExportRepo(ctx, "repo1", &buf))
	archived := buf.Bytes()

	// First import.
	dst := buildBackupSvc()
	stats1, err := dst.ImportRepo(ctx, bytes.NewReader(archived), "", "skip")
	require.NoError(t, err)
	assert.Equal(t, 1, stats1.Components)
	assert.Equal(t, 1, stats1.Assets)

	// Second import (same archive) — everything already exists.
	stats2, err := dst.ImportRepo(ctx, bytes.NewReader(archived), "", "skip")
	require.NoError(t, err)
	assert.Equal(t, 0, stats2.Components, "second import should skip existing components")
	assert.Equal(t, 0, stats2.Assets, "second import should skip existing assets")
}

func TestBackupRepo_ImportRename(t *testing.T) {
	ctx := context.Background()
	src := buildBackupSvc(testutil.SimpleRepo("original", "raw"))

	var buf bytes.Buffer
	require.NoError(t, src.ExportRepo(ctx, "original", &buf))

	dst := buildBackupSvc()
	stats, err := dst.ImportRepo(ctx, &buf, "renamed", "rename")
	require.NoError(t, err)
	assert.Equal(t, "renamed", stats.Repository)
}

func TestBackupRepo_ImportRenameConflict(t *testing.T) {
	ctx := context.Background()
	src := buildBackupSvc(testutil.SimpleRepo("repo", "raw"))

	var buf bytes.Buffer
	require.NoError(t, src.ExportRepo(ctx, "repo", &buf))

	// Destination already has "newname".
	dst := buildBackupSvc(testutil.SimpleRepo("newname", "raw"))
	_, err := dst.ImportRepo(ctx, bytes.NewReader(buf.Bytes()), "newname", "rename")
	assert.ErrorIs(t, err, service.ErrRepoConflict)
}

func TestBackupRepo_ImportRenameMissingTargetName(t *testing.T) {
	ctx := context.Background()
	src := buildBackupSvc(testutil.SimpleRepo("repo", "raw"))

	var buf bytes.Buffer
	require.NoError(t, src.ExportRepo(ctx, "repo", &buf))

	dst := buildBackupSvc()
	_, err := dst.ImportRepo(ctx, &buf, "", "rename")
	assert.Error(t, err)
	assert.NotErrorIs(t, err, service.ErrRepoConflict)
}
