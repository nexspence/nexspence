package service

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/storage"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// shiftingStore is a store whose blob is overwritten with a different size
// between the read and the post-copy re-check — what a client upload does to
// the source store during a migration, since the repository still points there
// until the very last row is done.
type shiftingStore struct {
	storage.BlobStore
	sizeAfter int64
	sizeErr   error
}

func (s shiftingStore) Size(_ context.Context, _ string) (int64, error) {
	if s.sizeErr != nil {
		return 0, s.sizeErr
	}
	return s.sizeAfter, nil
}

// Losing the race must not repoint the asset: the row would name the target
// while the target holds the bytes read before the upload landed (#298).
func TestBlobStoreMigration_CopyBlob_AbortsWhenSourceChangesMidCopy(t *testing.T) {
	ctx := context.Background()
	source := testutil.NewBlobStore()
	target := testutil.NewBlobStore()
	require.NoError(t, source.Put(ctx, "k", strings.NewReader("original"), 8))

	svc := &BlobStoreMigrationService{}
	_, err := svc.copyBlob(ctx, shiftingStore{BlobStore: source, sizeAfter: 7}, target, "k")

	require.ErrorContains(t, err, "changed during migration")
	require.False(t, target.Has("k"), "the stale staged copy must not be left in the target")
}

// A source that cannot be re-measured is the same situation with less
// information: refuse rather than repoint on an unverified copy.
func TestBlobStoreMigration_CopyBlob_AbortsWhenSourceCannotBeReChecked(t *testing.T) {
	ctx := context.Background()
	source := testutil.NewBlobStore()
	target := testutil.NewBlobStore()
	require.NoError(t, source.Put(ctx, "k", strings.NewReader("original"), 8))

	svc := &BlobStoreMigrationService{}
	_, err := svc.copyBlob(ctx, shiftingStore{BlobStore: source, sizeErr: errors.New("boom")}, target, "k")

	require.ErrorContains(t, err, "re-checking source blob")
	require.False(t, target.Has("k"))
}

// The unchanged case still copies the bytes through.
func TestBlobStoreMigration_CopyBlob_CopiesUnchangedBlob(t *testing.T) {
	ctx := context.Background()
	source := testutil.NewBlobStore()
	target := testutil.NewBlobStore()
	require.NoError(t, source.Put(ctx, "k", strings.NewReader("original"), 8))

	svc := &BlobStoreMigrationService{}
	n, err := svc.copyBlob(ctx, source, target, "k")
	require.NoError(t, err)
	require.Equal(t, int64(8), n)

	rc, _, err := target.Get(ctx, "k")
	require.NoError(t, err)
	defer func() { _ = rc.Close() }()
	got, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.Equal(t, "original", string(got))
}
