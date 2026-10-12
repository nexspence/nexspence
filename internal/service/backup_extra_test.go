package service_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// An archived component replaces the metadata an existing component of the same
// coordinates holds, even when the archive recorded none (#668): its empty
// extra decodes as nil, which on its own would keep the stale map.

func archivedPlainComponent(t *testing.T, export func(*bytes.Buffer) error, seed func() error) *bytes.Buffer {
	t.Helper()
	require.NoError(t, seed())
	var buf bytes.Buffer
	require.NoError(t, export(&buf))
	return &buf
}

func staleComponent(t *testing.T, ctx context.Context, svc interface {
	Create(context.Context, *domain.Component) error
}, repo *domain.Repository) *domain.Component {
	t.Helper()
	stale := &domain.Component{RepositoryID: repo.ID, Repository: repo.Name, Format: "raw", Name: "f.txt", Version: "1",
		Extra: map[string]any{"scan_result": map[string]any{"status": "ok"}}}
	require.NoError(t, svc.Create(ctx, stale))
	return stale
}

func TestBackup_ImportRepo_Overwrite_ArchivedEmptyExtraReplacesStale(t *testing.T) {
	ctx := context.Background()
	srcRepo := testutil.SimpleRepo("srcrepo", "raw")
	src := buildBackupSvc(srcRepo)
	buf := archivedPlainComponent(t,
		func(b *bytes.Buffer) error { return src.ExportRepo(ctx, "srcrepo", b) },
		func() error {
			return src.Components.Create(ctx, &domain.Component{RepositoryID: srcRepo.ID, Repository: "srcrepo", Format: "raw", Name: "f.txt", Version: "1"})
		})

	dstRepo := testutil.SimpleRepo("srcrepo", "raw")
	dst := buildBackupSvc(dstRepo)
	stale := staleComponent(t, ctx, dst.Components, dstRepo)

	_, err := dst.ImportRepo(ctx, buf, "", "overwrite")
	require.NoError(t, err)
	got, err := dst.Components.Get(ctx, stale.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Empty(t, got.Extra)
}

func TestBackup_Restore_ArchivedEmptyExtraReplacesStale(t *testing.T) {
	ctx := context.Background()
	srcRepo := testutil.SimpleRepo("srcrepo", "raw")
	src := buildBackupSvc(srcRepo)
	buf := archivedPlainComponent(t,
		func(b *bytes.Buffer) error { return src.Export(ctx, b) },
		func() error {
			return src.Components.Create(ctx, &domain.Component{RepositoryID: srcRepo.ID, Repository: "srcrepo", Format: "raw", Name: "f.txt", Version: "1"})
		})

	dstRepo := testutil.SimpleRepo("srcrepo", "raw")
	dst := buildBackupSvc(dstRepo)
	stale := staleComponent(t, ctx, dst.Components, dstRepo)

	_, err := dst.Restore(ctx, buf)
	require.NoError(t, err)
	got, err := dst.Components.Get(ctx, stale.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Empty(t, got.Extra)
}
