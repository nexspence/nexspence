package storage_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/storage"
)

func TestNewLocalBlobStore_CreatesDir(t *testing.T) {
	dir := t.TempDir() + "/blobs"
	store, err := storage.NewLocalBlobStore(dir)
	require.NoError(t, err)
	require.NotNil(t, store)
}

func TestNewLocalBlobStore_InvalidPath(t *testing.T) {
	// A path whose parent is a file (not a dir) cannot be created.
	dir := t.TempDir() + "/file"
	require.NoError(t, writeFile(t, dir, "x"))
	_, err := storage.NewLocalBlobStore(dir + "/sub")
	require.Error(t, err)
}

func TestLocalBlobStore_PutGet_Roundtrip(t *testing.T) {
	store := newLocal(t)
	ctx := context.Background()
	key := "abcdef1234567890"
	data := []byte("hello blob")

	require.NoError(t, store.Put(ctx, key, bytes.NewReader(data), int64(len(data))))

	rc, size, err := store.Get(ctx, key)
	require.NoError(t, err)
	defer rc.Close()
	assert.EqualValues(t, len(data), size)
	got, _ := io.ReadAll(rc)
	assert.Equal(t, data, got)
}

// Two clients racing to store the same blob key (a proxy cache-fill stampede,
// or two pushes of the same artifact) must never mix their bytes: whichever
// writer publishes last, the blob on disk must be exactly one writer's payload,
// and no writer may fail because another one was staging at the same time.
func TestLocalBlobStore_Put_ConcurrentSameKey_NoCorruption(t *testing.T) {
	store := newLocal(t)
	ctx := context.Background()
	const (
		key     = "abcdef1234567890"
		writers = 20
		// Bigger than io.Copy's 32 KiB buffer so the writes interleave instead of
		// completing in a single syscall.
		payloadSize = 256 * 1024
	)

	payloads := make([][]byte, writers)
	for i := range payloads {
		payloads[i] = bytes.Repeat([]byte{byte('a' + i)}, payloadSize)
	}

	var wg sync.WaitGroup
	errs := make([]error, writers)
	start := make(chan struct{})
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = store.Put(ctx, key, bytes.NewReader(payloads[i]), payloadSize)
		}()
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		require.NoErrorf(t, err, "writer %d must not fail because of a concurrent writer", i)
	}

	rc, size, err := store.Get(ctx, key)
	require.NoError(t, err)
	defer rc.Close()
	got, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.EqualValues(t, payloadSize, size, "the published blob must have exactly one payload's size")
	assert.Contains(t, payloads, got, "the published blob must be one writer's payload, not a mix of several")
}

func TestLocalBlobStore_Put_ShortKey(t *testing.T) {
	// key shorter than 4 chars uses flat path (no sharding)
	store := newLocal(t)
	ctx := context.Background()
	require.NoError(t, store.Put(ctx, "ab", bytes.NewReader([]byte("x")), 1))
	rc, _, err := store.Get(ctx, "ab")
	require.NoError(t, err)
	rc.Close()
}

func TestLocalBlobStore_Get_NotFound(t *testing.T) {
	store := newLocal(t)
	_, _, err := store.Get(context.Background(), "nonexistent")
	require.Error(t, err)
}

// A blob key carrying "../" segments (e.g. from a crafted backup/import
// archive) must be rejected and must not write outside the blob store root.
func TestLocalBlobStore_Put_PathTraversal_Rejected(t *testing.T) {
	base := t.TempDir() + "/blobs"
	store, err := storage.NewLocalBlobStore(base)
	require.NoError(t, err)
	ctx := context.Background()

	escaped := base + "/../pwned"
	defer os.Remove(escaped)

	traversalKey := "../../pwned"
	err = store.Put(ctx, traversalKey, bytes.NewReader([]byte("malicious")), 9)
	require.Error(t, err, "Put must reject a key that escapes the blob store root")
	assert.Contains(t, err.Error(), "outside blob store")

	_, statErr := os.Stat(escaped)
	assert.True(t, os.IsNotExist(statErr), "no file should be written outside the blob store root")

	// Get/Delete/Exists/Size must reject the traversal key too.
	_, _, err = store.Get(ctx, traversalKey)
	require.Error(t, err)
	require.Error(t, store.Delete(ctx, traversalKey))
	_, err = store.Exists(ctx, traversalKey)
	require.Error(t, err)
	_, err = store.Size(ctx, traversalKey)
	require.Error(t, err)
}

// A full disk must surface as a distinguishable sentinel so callers can map it
// to 507 Insufficient Storage instead of an opaque 500.
func TestLocalBlobStore_Put_DiskFull_ReturnsErrNoSpace(t *testing.T) {
	store := newLocal(t)

	err := store.Put(context.Background(), "abcdef1234567890", failingReader{err: syscall.ENOSPC}, 10)

	require.Error(t, err)
	assert.ErrorIs(t, err, storage.ErrNoSpace)
}

func TestLocalBlobStore_Put_OtherWriteError_IsNotNoSpace(t *testing.T) {
	store := newLocal(t)

	err := store.Put(context.Background(), "abcdef1234567890", failingReader{err: errors.New("boom")}, 10)

	require.Error(t, err)
	assert.NotErrorIs(t, err, storage.ErrNoSpace)
}

func TestLocalBlobStore_Delete_Existing(t *testing.T) {
	store := newLocal(t)
	ctx := context.Background()
	key := "deadbeef12345678"
	require.NoError(t, store.Put(ctx, key, bytes.NewReader([]byte("bye")), 3))
	require.NoError(t, store.Delete(ctx, key))
	exists, err := store.Exists(ctx, key)
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestLocalBlobStore_Delete_NotFound_NoError(t *testing.T) {
	store := newLocal(t)
	require.NoError(t, store.Delete(context.Background(), "missing"))
}

func TestLocalBlobStore_Exists(t *testing.T) {
	store := newLocal(t)
	ctx := context.Background()
	key := "cafebabe12345678"

	ok, err := store.Exists(ctx, key)
	require.NoError(t, err)
	assert.False(t, ok)

	require.NoError(t, store.Put(ctx, key, bytes.NewReader([]byte("y")), 1))
	ok, err = store.Exists(ctx, key)
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestLocalBlobStore_Size(t *testing.T) {
	store := newLocal(t)
	ctx := context.Background()
	key := "1234abcd5678ef90"
	data := []byte("sizeme")
	require.NoError(t, store.Put(ctx, key, bytes.NewReader(data), int64(len(data))))
	sz, err := store.Size(ctx, key)
	require.NoError(t, err)
	assert.EqualValues(t, len(data), sz)
}

func TestLocalBlobStore_Size_NotFound(t *testing.T) {
	store := newLocal(t)
	_, err := store.Size(context.Background(), "ghost")
	require.Error(t, err)
}

func TestLocalBlobStore_ListKeys_Empty(t *testing.T) {
	store := newLocal(t)
	keys, err := store.ListKeys(context.Background())
	require.NoError(t, err)
	assert.Empty(t, keys)
}

func TestLocalBlobStore_ListKeys_Multiple(t *testing.T) {
	store := newLocal(t)
	ctx := context.Background()
	put := []string{"aabbccdd11223344", "eeff99887766aabb", "12345678abcdef01"}
	for _, k := range put {
		require.NoError(t, store.Put(ctx, k, bytes.NewReader([]byte("x")), 1))
	}
	keys, err := store.ListKeys(ctx)
	require.NoError(t, err)
	assert.Len(t, keys, len(put))
	for _, k := range put {
		assert.Contains(t, keys, k)
	}
}

func TestLocalBlobStore_ListEntries(t *testing.T) {
	store := newLocal(t)
	ctx := context.Background()
	require.NoError(t, store.Put(ctx, "abcdef01", bytes.NewReader([]byte("hello")), 5))

	entries, err := store.ListEntries(ctx)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	e := entries[0]
	assert.Equal(t, "abcdef01", e.Key)
	assert.EqualValues(t, 5, e.Size)
	assert.False(t, e.ModTime.IsZero(), "mod time must not be zero")
	assert.LessOrEqual(t, time.Since(e.ModTime), time.Minute, "mod time must be recent")
}

func TestLocalBlobStore_UsedBytes_Empty(t *testing.T) {
	store := newLocal(t)
	n, err := store.UsedBytes(context.Background())
	require.NoError(t, err)
	assert.EqualValues(t, 0, n)
}

func TestLocalBlobStore_UsedBytes_AfterPut(t *testing.T) {
	store := newLocal(t)
	ctx := context.Background()
	data := strings.Repeat("a", 100)
	require.NoError(t, store.Put(ctx, "aabbccddeeff0011", bytes.NewReader([]byte(data)), int64(len(data))))
	n, err := store.UsedBytes(ctx)
	require.NoError(t, err)
	assert.EqualValues(t, 100, n)
}

// ── helpers ───────────────────────────────────────────────────────────────────

func newLocal(t *testing.T) *storage.LocalBlobStore {
	t.Helper()
	s, err := storage.NewLocalBlobStore(t.TempDir())
	require.NoError(t, err)
	return s
}

// failingReader fails every read with err, standing in for an I/O error hit
// while streaming a blob to disk (e.g. ENOSPC on a full filesystem).
type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

// writeFile creates a plain file at path with content (used to create a
// path that is a file so sub-directory creation fails).
func writeFile(t *testing.T, path, content string) error {
	t.Helper()
	return os.WriteFile(path, []byte(content), 0o600)
}
