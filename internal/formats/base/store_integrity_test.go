package base_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// ── declared size vs. bytes actually received ─────────────────

// A caller that declares more bytes than it sends must not get a component
// registered at the declared size: the recorded size and the recorded checksum
// would then describe different bytes, and every later download would announce
// a Content-Length it cannot deliver.
func TestStoreArtifact_DeclaredSizeLargerThanBody_Rejected(t *testing.T) {
	repo := testutil.SimpleRepo("cargo-hosted", "cargo")
	d, blobStore, comps, assets := deps(repo)

	_, err := base.StoreArtifact(context.Background(), d,
		"cargo-hosted", "/crates/x/x-1.0.crate", "application/x-tar",
		base.Coords{Name: "x", Version: "1.0"},
		strings.NewReader("only-ten!!"), 1_000_000)

	require.Error(t, err)
	assert.True(t, errors.Is(err, base.ErrSizeMismatch), "got %v", err)
	assert.Equal(t, http.StatusBadRequest, base.HTTPStatusForError(err),
		"a body contradicting its own declared length is the caller's error")

	page, err := assets.List(context.Background(), "cargo-hosted", 10, 0)
	require.NoError(t, err)
	assert.Empty(t, page.Items, "no asset row may survive a rejected store")
	comping, err := comps.List(context.Background(), "cargo-hosted", 10, 0)
	require.NoError(t, err)
	assert.Empty(t, comping.Items, "no component may survive a rejected store")
	assert.NotEmpty(t, blobStore.Deleted, "the partial blob must be cleaned up")
}

// The mirror case: more bytes arrive than were declared.
func TestStoreArtifact_DeclaredSizeSmallerThanBody_Rejected(t *testing.T) {
	repo := testutil.SimpleRepo("raw-hosted", "raw")
	d, _, _, _ := deps(repo)

	_, err := base.StoreArtifact(context.Background(), d,
		"raw-hosted", "/f.bin", "application/octet-stream",
		base.Coords{Name: "f.bin"},
		strings.NewReader("much-longer-than-declared"), 4)

	require.Error(t, err)
	assert.True(t, errors.Is(err, base.ErrSizeMismatch), "got %v", err)
}

// A body that matches its declared length is stored at that size, unchanged.
func TestStoreArtifact_DeclaredSizeMatches_Stored(t *testing.T) {
	repo := testutil.SimpleRepo("raw-hosted", "raw")
	d, blobStore, _, _ := deps(repo)

	body := "exactly-these-bytes"
	res, err := base.StoreArtifact(context.Background(), d,
		"raw-hosted", "/f.bin", "application/octet-stream",
		base.Coords{Name: "f.bin"},
		strings.NewReader(body), int64(len(body)))

	require.NoError(t, err)
	assert.Equal(t, int64(len(body)), res.Size)
	assert.Empty(t, blobStore.Deleted)

	rc, size, err := blobStore.Get(context.Background(), res.Asset.BlobKey)
	require.NoError(t, err)
	defer rc.Close()
	stored, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.Equal(t, body, string(stored))
	assert.Equal(t, res.Size, size, "the recorded size describes the stored bytes")
}

// An undeclared size (chunked upload) still takes its size from the store.
func TestStoreArtifact_UndeclaredSize_StillStores(t *testing.T) {
	repo := testutil.SimpleRepo("raw-hosted", "raw")
	d, _, _, _ := deps(repo)

	res, err := base.StoreArtifact(context.Background(), d,
		"raw-hosted", "/stream.bin", "application/octet-stream",
		base.Coords{Name: "stream.bin"},
		strings.NewReader("streamed"), -1)

	require.NoError(t, err)
	assert.Equal(t, int64(len("streamed")), res.Size)
}

// ── delete ordering ───────────────────────────────────────────

// The DB row is the source of truth, so it goes first. When its delete fails,
// the bytes must still be there: a row pointing at deleted bytes is not
// self-healing, while an orphaned blob is reclaimed by the blob GC.
func TestDeleteArtifact_DBFailureKeepsTheBytes(t *testing.T) {
	repo := testutil.SimpleRepo("raw-hosted", "raw")
	d, blobStore, _, assets := deps(repo)
	ctx := context.Background()

	res, err := base.StoreArtifact(ctx, d, "raw-hosted", "/f.bin", "application/octet-stream",
		base.Coords{Name: "f.bin"}, strings.NewReader("payload"), int64(len("payload")))
	require.NoError(t, err)

	assets.DeleteErr = errors.New("db timeout")
	err = base.DeleteArtifact(ctx, d, "raw-hosted", "/f.bin")

	require.Error(t, err)
	assert.Empty(t, blobStore.Deleted, "the blob must survive a failed row delete")
	exists, existsErr := blobStore.Exists(ctx, res.Asset.BlobKey)
	require.NoError(t, existsErr)
	assert.True(t, exists, "the row still names these bytes, so they must still be readable")
}

// The success path is unchanged by the reorder: row gone, bytes gone, usage
// decremented.
func TestDeleteArtifact_SuccessRemovesRowAndBytes(t *testing.T) {
	bs := &domain.BlobStore{ID: "bs1", Name: "default", Type: "local", Config: map[string]any{}}
	blobs := testutil.NewBlobStoreRepo(bs)
	repo := testutil.SimpleRepo("raw-hosted", "raw")
	d, blobStore, _, assets := deps(repo)
	d.Blobs = blobs
	ctx := context.Background()

	body := "payload"
	res, err := base.StoreArtifact(ctx, d, "raw-hosted", "/f.bin", "application/octet-stream",
		base.Coords{Name: "f.bin"}, strings.NewReader(body), int64(len(body)))
	require.NoError(t, err)

	require.NoError(t, base.DeleteArtifact(ctx, d, "raw-hosted", "/f.bin"))

	_, err = assets.GetByPath(ctx, "raw-hosted", "/f.bin")
	assert.Error(t, err, "the row is gone")
	exists, existsErr := blobStore.Exists(ctx, res.Asset.BlobKey)
	require.NoError(t, existsErr)
	assert.False(t, exists, "the bytes are gone")

	after, err := blobs.GetByID(ctx, "bs1")
	require.NoError(t, err)
	assert.Zero(t, after.UsedBytes, "usage is decremented once the bytes actually left")
}
