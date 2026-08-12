package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// syncCall records one fsync performed by Put and whether the blob was already
// visible at its final path when the fsync happened.
type syncCall struct {
	name      string
	dstExists bool
}

// recordSyncs replaces the store's fsync hook with one that records every call
// (and still performs the real fsync), returning a pointer to the call log.
func recordSyncs(s *LocalBlobStore, dst string) *[]syncCall {
	calls := make([]syncCall, 0, 2)
	s.syncFile = func(f *os.File) error {
		_, statErr := os.Stat(dst)
		calls = append(calls, syncCall{name: f.Name(), dstExists: statErr == nil})
		return f.Sync()
	}
	return &calls
}

func TestLocalBlobStore_Put_SyncsBlobBeforeRename(t *testing.T) {
	base := t.TempDir()
	s, err := NewLocalBlobStore(base)
	require.NoError(t, err)

	const key = "abcdef1234567890"
	dst := filepath.Join(base, "ab", "cd", key)
	calls := recordSyncs(s, dst)

	require.NoError(t, s.Put(context.Background(), key, strings.NewReader("payload"), 7))

	require.NotEmpty(t, *calls, "Put must fsync the blob contents before renaming it into place")
	first := (*calls)[0]
	assert.True(t, strings.HasPrefix(first.name, dst+".tmp."),
		"the staged temp file must be the first thing fsynced, got %q", first.name)
	assert.False(t, first.dstExists, "the fsync must happen before the rename, not after")
}

// Every Put must stage under its own temp path. Sharing one fixed temp name
// lets two concurrent writers for the same key write into the same inode, so
// the loser corrupts the blob the winner already published (issue #196).
func TestLocalBlobStore_Put_StagesAtUniqueTempPath(t *testing.T) {
	base := t.TempDir()
	s, err := NewLocalBlobStore(base)
	require.NoError(t, err)

	const key = "abcdef1234567890"
	dst := filepath.Join(base, "ab", "cd", key)
	calls := recordSyncs(s, dst)

	require.NoError(t, s.Put(context.Background(), key, strings.NewReader("first"), 5))
	require.NoError(t, s.Put(context.Background(), key, strings.NewReader("second"), 6))

	require.Len(t, *calls, 4, "each Put must fsync the blob and then its parent directory")
	assert.NotEqual(t, (*calls)[0].name, (*calls)[2].name,
		"two Puts for the same key must not stage at the same temp path")
}

func TestLocalBlobStore_Put_SyncsParentDirAfterRename(t *testing.T) {
	base := t.TempDir()
	s, err := NewLocalBlobStore(base)
	require.NoError(t, err)

	const key = "abcdef1234567890"
	dst := filepath.Join(base, "ab", "cd", key)
	calls := recordSyncs(s, dst)

	require.NoError(t, s.Put(context.Background(), key, strings.NewReader("payload"), 7))

	require.Len(t, *calls, 2, "Put must fsync the blob and then its parent directory")
	last := (*calls)[1]
	assert.Equal(t, filepath.Dir(dst), last.name, "the parent directory must be fsynced so the rename is durable")
	assert.True(t, last.dstExists, "the directory fsync must happen after the rename")
}

func TestLocalBlobStore_Put_SyncFailure_LeavesNoBlob(t *testing.T) {
	base := t.TempDir()
	s, err := NewLocalBlobStore(base)
	require.NoError(t, err)

	const key = "abcdef1234567890"
	dst := filepath.Join(base, "ab", "cd", key)
	syncErr := errors.New("fsync failed")
	s.syncFile = func(f *os.File) error {
		if strings.HasPrefix(f.Name(), dst+".tmp.") {
			return syncErr
		}
		return f.Sync()
	}

	err = s.Put(context.Background(), key, strings.NewReader("payload"), 7)

	require.ErrorIs(t, err, syncErr, "a failed fsync must fail the Put")
	assert.NoFileExists(t, dst, "no blob may be published when its contents were not flushed to disk")
	leftovers, globErr := filepath.Glob(dst + ".tmp.*")
	require.NoError(t, globErr)
	assert.Empty(t, leftovers, "the temp file must be cleaned up")
}

func TestLocalBlobStore_Put_DirSyncFailure_IsNotFatal(t *testing.T) {
	base := t.TempDir()
	s, err := NewLocalBlobStore(base)
	require.NoError(t, err)

	const key = "abcdef1234567890"
	dst := filepath.Join(base, "ab", "cd", key)
	s.syncFile = func(f *os.File) error {
		if f.Name() == filepath.Dir(dst) {
			return errors.New("fsync on directory not supported")
		}
		return f.Sync()
	}

	// Some filesystems reject fsync on a directory; the blob itself is already
	// durable at that point, so Put must still succeed.
	require.NoError(t, s.Put(context.Background(), key, strings.NewReader("payload"), 7))
	assert.FileExists(t, dst)
}
