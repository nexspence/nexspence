package base_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/storage"
	"github.com/nexspence-oss/nexspence/internal/testutil"
)

// ReadCachedBlob reads from the store that holds the asset, honors the limit,
// and answers nil for anything it cannot read.
func TestReadCachedBlob(t *testing.T) {
	ctx := context.Background()
	repo := testutil.SimpleRepo("cached", "raw")
	d, _, _, _ := deps(repo)
	root := t.TempDir()
	fallback, err := storage.NewLocalBlobStore(root)
	require.NoError(t, err)
	d.BlobStore, d.Registry = fallback, storage.NewRegistry(fallback)
	named := &domain.BlobStore{ID: "named-id", Name: "named", Type: "local", Config: map[string]any{"path": filepath.Join(root, "named")}}
	d.Blobs = testutil.NewBlobStoreRepo(named)

	require.NoError(t, fallback.Put(ctx, "k1", strings.NewReader("hello world"), 11))
	assert.Equal(t, "hello world", string(base.ReadCachedBlob(ctx, d, &domain.Asset{BlobKey: "k1"}, 1<<10)))
	assert.Equal(t, "hello", string(base.ReadCachedBlob(ctx, d, &domain.Asset{BlobKey: "k1"}, 5)), "limit")

	physical, err := base.PhysicalStore(ctx, d, named)
	require.NoError(t, err)
	require.NoError(t, physical.Put(ctx, "k2", strings.NewReader("in named"), 8))
	assert.Equal(t, "in named", string(base.ReadCachedBlob(ctx, d, &domain.Asset{BlobKey: "k2", BlobStoreID: named.ID}, 1<<10)))

	assert.Nil(t, base.ReadCachedBlob(ctx, d, &domain.Asset{BlobKey: "missing"}, 1<<10), "missing blob")
	assert.Nil(t, base.ReadCachedBlob(ctx, d, &domain.Asset{BlobKey: "k1", BlobStoreID: "no-such-store"}, 1<<10), "unknown store")
}
