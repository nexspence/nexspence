package storage_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/storage"
)

func TestProbeLocalWritable_ExistingDir(t *testing.T) {
	require.NoError(t, storage.ProbeLocalWritable(t.TempDir()))
}

func TestProbeLocalWritable_CreatesMissingDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "new-store")
	require.NoError(t, storage.ProbeLocalWritable(dir))
	info, err := os.Stat(dir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestProbeLocalWritable_NotWritable(t *testing.T) {
	dir := unwritableDir(t)
	err := storage.ProbeLocalWritable(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "permission denied")
}

func unwritableDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0o555))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	f, err := os.CreateTemp(dir, "probe-*")
	if err == nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		t.Skip("directory is still writable after chmod 0555")
	}
	return dir
}
