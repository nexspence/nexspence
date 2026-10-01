package service_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// A page of components or assets that fails to load fails the export: an
// archive cut short there would pass for a complete backup (#569).

var errPageLost = errors.New("FATAL: terminating connection due to administrator command (SQLSTATE 57P01)")

// assetListFailing fails every asset page of one repository.
type assetListFailing struct {
	*testutil.AssetRepo
	repo string
}

func (a assetListFailing) List(ctx context.Context, repoName string, limit, offset int) (*domain.Page[domain.Asset], error) {
	if repoName == a.repo {
		return nil, errPageLost
	}
	return a.AssetRepo.List(ctx, repoName, limit, offset)
}

// componentListFailing fails every component page of one repository.
type componentListFailing struct {
	*testutil.ComponentRepo
	repo string
}

func (c componentListFailing) List(ctx context.Context, repoName string, limit, offset int) (*domain.Page[domain.Component], error) {
	if repoName == c.repo {
		return nil, errPageLost
	}
	return c.ComponentRepo.List(ctx, repoName, limit, offset)
}

// seededPageErrorSvc returns a backup service over two repositories with one
// asset each; the caller swaps in a failing lister.
func seededPageErrorSvc(t *testing.T) *service.BackupService {
	t.Helper()
	good := testutil.SimpleRepo("good", "raw")
	bad := testutil.SimpleRepo("bad", "raw")
	svc := buildBackupSvc(good, bad)
	seedAsset(t, svc, good, "/a.txt", "", "")
	seedAsset(t, svc, bad, "/b.txt", "", "")
	return svc
}

func failAssets(svc *service.BackupService, repo string) {
	svc.Assets = assetListFailing{AssetRepo: svc.Assets.(*testutil.AssetRepo), repo: repo}
}

func failComponents(svc *service.BackupService, repo string) {
	svc.Components = componentListFailing{ComponentRepo: svc.Components.(*testutil.ComponentRepo), repo: repo}
}

func TestBackup_Export_AssetPageError_FailsExport(t *testing.T) {
	svc := seededPageErrorSvc(t)
	failAssets(svc, "bad")

	err := svc.Export(context.Background(), &bytes.Buffer{})
	require.ErrorIs(t, err, errPageLost)
	assert.Contains(t, err.Error(), "list assets of bad")
	var incomplete *service.IncompleteBackupError
	assert.False(t, errors.As(err, &incomplete), "a lost page is a hard failure, not missing blobs")
}

func TestBackup_Export_ComponentPageError_FailsExport(t *testing.T) {
	svc := seededPageErrorSvc(t)
	failComponents(svc, "bad")

	err := svc.Export(context.Background(), &bytes.Buffer{})
	require.ErrorIs(t, err, errPageLost)
	assert.Contains(t, err.Error(), "list components of bad")
}

func TestBackup_ExportRepo_AssetPageError_FailsExport(t *testing.T) {
	svc := seededPageErrorSvc(t)
	failAssets(svc, "bad")

	err := svc.ExportRepo(context.Background(), "bad", &bytes.Buffer{})
	require.ErrorIs(t, err, errPageLost)
	assert.Contains(t, err.Error(), "list assets of bad")
}

func TestBackup_ExportRepo_ComponentPageError_FailsExport(t *testing.T) {
	svc := seededPageErrorSvc(t)
	failComponents(svc, "bad")

	err := svc.ExportRepo(context.Background(), "bad", &bytes.Buffer{})
	require.ErrorIs(t, err, errPageLost)
	assert.Contains(t, err.Error(), "list components of bad")
}

// The repository that does not fail still exports normally.
func TestBackup_ExportRepo_OtherRepoUnaffected(t *testing.T) {
	svc := seededPageErrorSvc(t)
	failAssets(svc, "bad")
	failComponents(svc, "bad")

	require.NoError(t, svc.ExportRepo(context.Background(), "good", &bytes.Buffer{}))
}

// A scheduled run that loses a page stores nothing and leaves the previous
// backup in place, even with retentionCount 1.
func TestBackupService_RunScheduled_PageError_KeepsPreviousBackup(t *testing.T) {
	ctx := context.Background()
	good := testutil.SimpleRepo("good", "raw")
	bad := testutil.SimpleRepo("bad", "raw")
	svc, settings := buildScheduledBackupSvc(good, bad)
	seedAsset(t, svc, good, "/a.txt", "", "")
	seedAsset(t, svc, bad, "/b.txt", "", "")
	failAssets(svc, "bad")

	dest := testutil.NewBlobStore()
	svc.Resolver = testutil.NewFakeResolver(dest)
	bs := &domain.BlobStore{ID: "bs-dest", Name: "backup-dest", Type: "s3"}
	require.NoError(t, svc.BlobStores.Create(ctx, bs))
	require.NoError(t, settings.Upsert(ctx, &domain.BackupSettings{
		Enabled: true, ScheduleCron: "0 3 * * *", BlobStoreID: bs.ID, RetentionCount: 1,
	}))
	const previous = "backups/nexspence-backup-20260101-000000.tar.gz"
	require.NoError(t, dest.Put(ctx, previous, strings.NewReader("good backup"), int64(len("good backup"))))
	dest.SetMTime(previous, time.Now().Add(-time.Hour))

	key, err := svc.RunScheduled(ctx)
	require.ErrorIs(t, err, errPageLost)
	assert.Empty(t, key)

	entries, err := dest.ListEntries(ctx)
	require.NoError(t, err)
	require.Len(t, entries, 1, "nothing new stored, nothing rotated out")
	_, _, err = dest.Get(ctx, previous)
	assert.NoError(t, err, "the last good backup must survive")
}
