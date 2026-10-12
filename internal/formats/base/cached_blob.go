package base

import (
	"context"
	"io"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
)

// ReadCachedBlob reads up to limit bytes of a stored asset from the blob store
// that holds it, or returns nil. Deliberately not FetchArtifact: that path
// counts a download, and an internal policy read (the minimum-package-age
// lookups) must not inflate the asset's statistics.
func ReadCachedBlob(ctx context.Context, d formats.Deps, asset *domain.Asset, limit int64) []byte {
	store := d.BlobStore
	if asset.BlobStoreID != "" {
		meta, err := d.Blobs.GetByID(ctx, asset.BlobStoreID)
		if err != nil || meta == nil {
			return nil
		}
		resolved, err := PhysicalStore(ctx, d, meta)
		if err != nil {
			return nil
		}
		store = resolved
	}
	rc, _, err := store.Get(ctx, asset.BlobKey)
	if err != nil {
		return nil
	}
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(io.LimitReader(rc, limit))
	if err != nil {
		return nil
	}
	return b
}
