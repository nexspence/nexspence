package storage_test

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/storage"
)

// appendable pins the compile-time contract: the local store is the fallback
// backend, so a chunked upload only streams if it satisfies the extension.
func appendable(t *testing.T, s *storage.LocalBlobStore) storage.AppendableBlobStore {
	t.Helper()
	as, ok := interface{}(s).(storage.AppendableBlobStore)
	require.True(t, ok, "LocalBlobStore must implement AppendableBlobStore")
	return as
}

func TestLocalBlobStore_AppendBlob_CreatesAndRoundtrips(t *testing.T) {
	store := newLocal(t)
	as := appendable(t, store)
	ctx := context.Background()
	key := "abcdef1234567890"
	data := []byte("first chunk")

	total, err := as.AppendBlob(ctx, key, bytes.NewReader(data))
	require.NoError(t, err)
	assert.EqualValues(t, len(data), total)

	rc, size, err := store.Get(ctx, key)
	require.NoError(t, err)
	defer rc.Close()
	assert.EqualValues(t, len(data), size)
	got, _ := io.ReadAll(rc)
	assert.Equal(t, data, got)
}

func TestLocalBlobStore_AppendBlob_AccumulatesAcrossCalls(t *testing.T) {
	store := newLocal(t)
	as := appendable(t, store)
	ctx := context.Background()
	key := "1122334455667788"

	chunks := []string{"alpha", "-beta", "-gamma"}
	var want string
	for _, c := range chunks {
		want += c
		total, err := as.AppendBlob(ctx, key, bytes.NewReader([]byte(c)))
		require.NoError(t, err)
		assert.EqualValues(t, len(want), total, "size after appending %q", c)
	}

	rc, _, err := store.Get(ctx, key)
	require.NoError(t, err)
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	assert.Equal(t, want, string(got))
}

// Appending onto a blob written by Put must extend it, not replace it: a
// session starts with Put of an empty object and grows from there.
func TestLocalBlobStore_AppendBlob_ExtendsPutBlob(t *testing.T) {
	store := newLocal(t)
	as := appendable(t, store)
	ctx := context.Background()
	key := "99aabbccddeeff00"

	require.NoError(t, store.Put(ctx, key, bytes.NewReader([]byte("head-")), 5))
	total, err := as.AppendBlob(ctx, key, bytes.NewReader([]byte("tail")))
	require.NoError(t, err)
	assert.EqualValues(t, 9, total)

	rc, _, err := store.Get(ctx, key)
	require.NoError(t, err)
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	assert.Equal(t, "head-tail", string(got))
}

func TestLocalBlobStore_TruncateBlob_RollsBackAndResumes(t *testing.T) {
	store := newLocal(t)
	as := appendable(t, store)
	ctx := context.Background()
	key := "deadbeefcafe0011"

	_, err := as.AppendBlob(ctx, key, bytes.NewReader([]byte("keep-drop")))
	require.NoError(t, err)
	require.NoError(t, as.TruncateBlob(ctx, key, 4))

	size, err := store.Size(ctx, key)
	require.NoError(t, err)
	assert.EqualValues(t, 4, size)

	// A truncated blob must still be appendable — the rejected chunk costs the
	// session nothing, so the next one continues from the rolled-back offset.
	total, err := as.AppendBlob(ctx, key, bytes.NewReader([]byte("-more")))
	require.NoError(t, err)
	assert.EqualValues(t, 9, total)

	rc, _, err := store.Get(ctx, key)
	require.NoError(t, err)
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	assert.Equal(t, "keep-more", string(got))
}

func TestLocalBlobStore_TruncateBlob_MissingKey(t *testing.T) {
	as := appendable(t, newLocal(t))
	require.Error(t, as.TruncateBlob(context.Background(), "0000111122223333", 0))
}

func TestLocalBlobStore_AppendedSize(t *testing.T) {
	store := newLocal(t)
	as := appendable(t, store)
	ctx := context.Background()
	key := "5566778899aabbcc"

	_, ok, err := as.AppendedSize(ctx, key)
	require.NoError(t, err)
	assert.False(t, ok, "unknown key must not report a staged size")

	_, err = as.AppendBlob(ctx, key, bytes.NewReader([]byte("1234567")))
	require.NoError(t, err)

	size, ok, err := as.AppendedSize(ctx, key)
	require.NoError(t, err)
	require.True(t, ok)
	assert.EqualValues(t, 7, size)
}

func TestLocalBlobStore_AppendedSize_InvalidKey(t *testing.T) {
	as := appendable(t, newLocal(t))
	_, _, err := as.AppendedSize(context.Background(), "../../escape")
	require.Error(t, err)
}

// Finalize and abort exist for backends that stage out of band; on local disk
// the bytes are live from the first append, so both are no-ops that must leave
// the blob exactly as it is.
func TestLocalBlobStore_FinalizeAndAbortAppend_AreNoOps(t *testing.T) {
	store := newLocal(t)
	as := appendable(t, store)
	ctx := context.Background()
	key := "aabb00112233ccdd"

	_, err := as.AppendBlob(ctx, key, bytes.NewReader([]byte("payload")))
	require.NoError(t, err)

	require.NoError(t, as.FinalizeAppend(ctx, key))
	require.NoError(t, as.AbortAppend(ctx, key))
	require.NoError(t, as.FinalizeAppend(ctx, "no-such-session"))

	size, err := store.Size(ctx, key)
	require.NoError(t, err)
	assert.EqualValues(t, 7, size)
}

func TestLocalBlobStore_AppendBlob_InvalidKey(t *testing.T) {
	as := appendable(t, newLocal(t))
	_, err := as.AppendBlob(context.Background(), "../../escape", bytes.NewReader([]byte("x")))
	require.Error(t, err)
}

func TestLocalBlobStore_AppendBlob_ReaderError(t *testing.T) {
	as := appendable(t, newLocal(t))
	_, err := as.AppendBlob(context.Background(), "eeff001122334455",
		failingReader{err: io.ErrUnexpectedEOF})
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
}

// The point of the extension is that a chunk costs O(chunk), not O(blob): a
// read-modify-write of 2000 chunks would move ~128 GiB through the process and
// the disk instead of 128 MiB. The bound is deliberately generous — it catches
// a return to quadratic behavior (or a per-call fsync, which measured ~60x
// slower here), not a few percent of disk performance.
func TestLocalBlobStore_AppendBlob_ManyChunksStayLinear(t *testing.T) {
	if testing.Short() {
		t.Skip("writes 128 MiB")
	}
	as := appendable(t, newLocal(t))
	ctx := context.Background()
	key := "77778888999900aa"
	chunk := bytes.Repeat([]byte("x"), 64*1024)

	start := time.Now()
	var total int64
	for i := 0; i < 2000; i++ {
		var err error
		total, err = as.AppendBlob(ctx, key, bytes.NewReader(chunk))
		require.NoError(t, err)
	}
	elapsed := time.Since(start)

	assert.EqualValues(t, 2000*len(chunk), total)
	assert.Less(t, elapsed, 5*time.Second, "2000 appends took %s — appends are not streaming", elapsed)
}
