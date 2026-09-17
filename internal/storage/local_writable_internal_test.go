package storage

import (
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClassifyLocalPathErr_EROFS(t *testing.T) {
	err := classifyLocalPathErr("/app/data/blobs/x", syscall.EROFS)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "EROFS")
	assert.Contains(t, err.Error(), "read-only")
}

func TestClassifyLocalPathErr_Permission(t *testing.T) {
	err := classifyLocalPathErr("/blobs/other", syscall.EACCES)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "permission denied")
}
