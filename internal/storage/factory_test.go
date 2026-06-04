package storage_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	nexspencecfg "github.com/nexspence-oss/nexspence/internal/config"
	"github.com/nexspence-oss/nexspence/internal/storage"
)

func TestNewBlobStoreFromConfig_LocalDefault(t *testing.T) {
	cfg := &nexspencecfg.Config{}
	cfg.Storage.DefaultType = ""
	cfg.Storage.Local.BasePath = t.TempDir()
	bs, err := storage.NewBlobStoreFromConfig(context.Background(), cfg)
	require.NoError(t, err)
	require.NotNil(t, bs)
}

func TestNewBlobStoreFromConfig_LocalExplicit(t *testing.T) {
	cfg := &nexspencecfg.Config{}
	cfg.Storage.DefaultType = "local"
	cfg.Storage.Local.BasePath = t.TempDir()
	bs, err := storage.NewBlobStoreFromConfig(context.Background(), cfg)
	require.NoError(t, err)
	require.NotNil(t, bs)
}

func TestNewBlobStoreFromConfig_LocalEmptyPath_UsesDefault(t *testing.T) {
	cfg := &nexspencecfg.Config{}
	cfg.Storage.DefaultType = "local"
	cfg.Storage.Local.BasePath = "" // uses ./data/blobs fallback
	bs, err := storage.NewBlobStoreFromConfig(context.Background(), cfg)
	require.NoError(t, err)
	require.NotNil(t, bs)
}

func TestNewBlobStoreFromConfig_S3MissingBucket_Error(t *testing.T) {
	cfg := &nexspencecfg.Config{}
	cfg.Storage.DefaultType = "s3"
	cfg.Storage.S3.Bucket = "" // missing → error
	_, err := storage.NewBlobStoreFromConfig(context.Background(), cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bucket")
}
