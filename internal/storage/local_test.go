package storage_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"

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

// writeFile creates a plain file at path with content (used to create a
// path that is a file so sub-directory creation fails).
func writeFile(t *testing.T, path, content string) error {
	t.Helper()
	return os.WriteFile(path, []byte(content), 0o600)
}
