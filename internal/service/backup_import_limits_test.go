package service

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildArchive writes a tar.gz with the given entries.
func buildArchive(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for name, data := range entries {
		require.NoError(t, tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o600, Size: int64(len(data)),
		}))
		_, err := tw.Write(data)
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())
	return buf.Bytes()
}

func TestBackupArchive_BlobsAreReadableAfterImport(t *testing.T) {
	raw := buildArchive(t, map[string][]byte{
		"repository.json": []byte(`{"name":"r"}`),
		"blobs/aa/bb/cc":  []byte("blob payload"),
	})

	a, err := readBackupArchive(bytes.NewReader(raw))
	require.NoError(t, err)
	t.Cleanup(func() { _ = a.Close() })

	require.True(t, a.hasBlob("aa/bb/cc"))
	rc, size, ok := a.openBlob("aa/bb/cc")
	require.True(t, ok)
	defer func() { _ = rc.Close() }()

	got, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.Equal(t, "blob payload", string(got))
	assert.Equal(t, int64(len("blob payload")), size)
}

// The whole point: blob payloads must not all be resident at once. An 8 GiB
// archive used to mean 8 GiB of process memory.
func TestBackupArchive_BlobsAreSpooledToDisk(t *testing.T) {
	raw := buildArchive(t, map[string][]byte{
		"repository.json": []byte(`{"name":"r"}`),
		"blobs/big":       bytes.Repeat([]byte("x"), 1<<20),
	})

	a, err := readBackupArchive(bytes.NewReader(raw))
	require.NoError(t, err)
	t.Cleanup(func() { _ = a.Close() })

	path, ok := a.blobPath("big")
	require.True(t, ok, "blob payloads should be spooled to disk, not held in memory")
	st, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, int64(1<<20), st.Size())
}

func TestBackupArchive_CloseRemovesSpooledFiles(t *testing.T) {
	raw := buildArchive(t, map[string][]byte{
		"repository.json": []byte(`{"name":"r"}`),
		"blobs/one":       []byte("payload"),
	})

	a, err := readBackupArchive(bytes.NewReader(raw))
	require.NoError(t, err)

	path, ok := a.blobPath("one")
	require.True(t, ok)
	require.NoError(t, a.Close())

	_, err = os.Stat(path)
	assert.True(t, os.IsNotExist(err), "Close must not leave the archive behind on disk")
}

// L-2: entry size was capped, entry count was not. A pathological archive of
// millions of tiny entries costs CPU and map growth without ever tripping the
// byte limit.
func TestBackupArchive_RejectsTooManyEntries(t *testing.T) {
	const cap = 3
	entries := map[string][]byte{"repository.json": []byte(`{"name":"r"}`)}
	for i := range cap + 1 {
		entries[fmt.Sprintf("blobs/%d", i)] = []byte("x")
	}
	raw := buildArchive(t, entries)

	a, err := readBackupArchiveWithLimits(bytes.NewReader(raw), defaultMaxImportBytes, cap)
	if a != nil {
		t.Cleanup(func() { _ = a.Close() })
	}
	require.Error(t, err)
	assert.Contains(t, err.Error(), "entries")
}

func TestBackupArchive_ByteLimitStillApplies(t *testing.T) {
	raw := buildArchive(t, map[string][]byte{
		"blobs/big": bytes.Repeat([]byte("x"), 4096),
	})

	a, err := readBackupArchiveLimited(bytes.NewReader(raw), 1024)
	if a != nil {
		t.Cleanup(func() { _ = a.Close() })
	}
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decompression limit")
}

// A failed read must not leave spool files behind.
func TestBackupArchive_CleansUpOnFailure(t *testing.T) {
	raw := buildArchive(t, map[string][]byte{
		"blobs/one": bytes.Repeat([]byte("x"), 4096),
		"blobs/two": bytes.Repeat([]byte("y"), 4096),
	})

	before := countTempDirs(t)
	_, err := readBackupArchiveLimited(bytes.NewReader(raw), 1024)
	require.Error(t, err)
	assert.Equal(t, before, countTempDirs(t), "spool directory should be removed on failure")
}

func countTempDirs(t *testing.T) int {
	t.Helper()
	names, err := os.ReadDir(os.TempDir())
	require.NoError(t, err)
	n := 0
	for _, e := range names {
		if e.IsDir() && len(e.Name()) > 15 && e.Name()[:15] == "nexspence-impor" {
			n++
		}
	}
	return n
}
