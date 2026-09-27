package service_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/service"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// A restore or import that had to leave something out says what, instead of
// answering with a normal summary (#551).

// repoCreateRefusing refuses to create the repository named refuse.
type repoCreateRefusing struct {
	*testutil.RepoRepo
	refuse string
}

func (r repoCreateRefusing) Create(ctx context.Context, repo *domain.Repository) error {
	if repo.Name == r.refuse {
		return errors.New("pq: insert violates foreign key constraint")
	}
	return r.RepoRepo.Create(ctx, repo)
}

// assetCreateRefusing refuses every asset insert.
type assetCreateRefusing struct{ *testutil.AssetRepo }

func (assetCreateRefusing) Create(context.Context, *domain.Asset) error {
	return errors.New("pq: connection reset")
}

func TestBackup_Restore_RepositoryCreateFailure_IsReported(t *testing.T) {
	ctx := context.Background()
	buf := exportWithS3Repo(t)

	dst := buildBackupSvc()
	dst.Resolver = testutil.NewFakeResolver(testutil.NewBlobStore())
	dst.Repos = repoCreateRefusing{RepoRepo: testutil.NewRepoRepo(), refuse: "s3repo"}

	stats, err := dst.Restore(ctx, buf)
	require.NoError(t, err)
	assert.Equal(t, 0, stats.Repos)
	assert.Equal(t, 1, stats.FailedItems, "only the repository is listed; its assets are covered by that entry")
	require.Len(t, stats.Failures, 1)
	assert.Equal(t, "repository", stats.Failures[0].Kind)
	assert.Equal(t, "s3repo", stats.Failures[0].Name)
	assert.Contains(t, stats.Failures[0].Error, "foreign key")
}

func TestBackup_Restore_BlobWriteFailure_IsReportedAsAsset(t *testing.T) {
	ctx := context.Background()
	buf := exportWithS3Repo(t)

	dst := buildBackupSvc()
	dst.Resolver = testutil.NewFakeResolver(putRefusingStore{testutil.NewBlobStore()})
	stats, err := dst.Restore(ctx, buf)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.BlobsFailed)
	require.Len(t, stats.Failures, 1)
	assert.Equal(t, service.RestoreFailure{
		Kind: "asset", Name: "s3repo/f.txt",
		Error: "write blob ab/cd/k1: s3: connection refused",
	}, stats.Failures[0])
}

func TestBackup_ImportRepo_AssetCreateFailure_IsReported(t *testing.T) {
	ctx := context.Background()
	buf := exportRepoArchive(t)

	dst := buildBackupSvc()
	dst.Assets = assetCreateRefusing{testutil.NewAssetRepo()}
	stats, err := dst.ImportRepo(ctx, buf, "", "skip")
	require.NoError(t, err)
	assert.Equal(t, 0, stats.Assets)
	assert.Equal(t, 1, stats.FailedItems)
	require.Len(t, stats.Failures, 1)
	assert.Equal(t, "asset", stats.Failures[0].Kind)
	assert.Equal(t, "imp/imp.bin", stats.Failures[0].Name)
}

// Every failure is counted, but the list in the response is capped.
func TestBackup_Restore_FailureListIsCapped(t *testing.T) {
	ctx := context.Background()
	repo := testutil.SimpleRepo("many", "raw")
	src := buildBackupSvc(repo)
	const n = 150
	for i := range n {
		seedAsset(t, src, repo, fmt.Sprintf("/f%03d.bin", i), "", "")
	}
	var buf bytes.Buffer
	require.NoError(t, src.Export(ctx, &buf))

	dst := buildBackupSvc()
	dst.Assets = assetCreateRefusing{testutil.NewAssetRepo()}
	stats, err := dst.Restore(ctx, &buf)
	require.NoError(t, err)
	assert.Equal(t, n, stats.FailedItems)
	assert.Len(t, stats.Failures, 100)
}
