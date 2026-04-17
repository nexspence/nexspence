// Package base provides shared artifact storage helpers used by all format handlers.
package base

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/metrics"
)

// StoreResult holds checksums and metadata after a successful store.
type StoreResult struct {
	Asset  *domain.Asset
	SHA256 string
	SHA1   string
	MD5    string
	Size   int64
}

// StoreArtifact streams reader into the blob store, computes checksums,
// and upserts the component + asset records in the DB.
// coords.Version may be empty for formats that don't have versions (e.g. raw).
func StoreArtifact(ctx context.Context, d formats.Deps,
	repoName, filePath, contentType string,
	coords Coords,
	reader io.Reader, declaredSize int64,
) (*StoreResult, error) {

	repo, err := d.Repos.Get(ctx, repoName)
	if err != nil || repo == nil {
		return nil, fmt.Errorf("repository %q not found", repoName)
	}
	if !repo.Online {
		return nil, fmt.Errorf("repository %q is offline", repoName)
	}

	// ── Quota check ───────────────────────────────────────────
	if repo.QuotaBytes != nil && *repo.QuotaBytes > 0 && declaredSize > 0 {
		bsName := resolveBlobStore(ctx, d, repo)
		if bs, err2 := d.Blobs.Get(ctx, bsName); err2 == nil && bs != nil {
			if bs.QuotaBytes != nil && *bs.QuotaBytes > 0 {
				if bs.UsedBytes+declaredSize > *bs.QuotaBytes {
					return nil, fmt.Errorf("storage quota exceeded for blob store %q (%d / %d bytes)",
						bsName, bs.UsedBytes, *bs.QuotaBytes)
				}
			}
		}
	}

	blobKey := BlobKey(repoName, filePath)

	// Stream → hash writers → blob store via pipe
	sha256h := sha256.New()
	sha1h   := sha1.New()
	md5h    := md5.New()

	pr, pw := io.Pipe()
	var pipeErr error
	go func() {
		_, pipeErr = io.Copy(io.MultiWriter(pw, sha256h, sha1h, md5h), reader)
		pw.CloseWithError(pipeErr)
	}()

	if err := d.BlobStore.Put(ctx, blobKey, pr, declaredSize); err != nil {
		return nil, fmt.Errorf("store blob: %w", err)
	}

	sha256sum := hex.EncodeToString(sha256h.Sum(nil))
	sha1sum   := hex.EncodeToString(sha1h.Sum(nil))
	md5sum    := hex.EncodeToString(md5h.Sum(nil))

	size := declaredSize
	if size <= 0 {
		if s, err := d.BlobStore.Size(ctx, blobKey); err == nil {
			size = s
		}
	}

	asset, err := RegisterStoredBlob(ctx, d, repo, filePath, contentType, coords, blobKey, sha256sum, sha1sum, md5sum, size)
	if err != nil {
		return nil, err
	}

	metrics.ArtifactsStored.Add(1)
	metrics.BytesStored.Add(size)

	return &StoreResult{
		Asset:  asset,
		SHA256: sha256sum,
		SHA1:   sha1sum,
		MD5:    md5sum,
		Size:   size,
	}, nil
}

// RegisterStoredBlob upserts component + asset after a blob was written to blobKey with known checksums.
func RegisterStoredBlob(ctx context.Context, d formats.Deps, repo *domain.Repository,
	filePath, contentType string, coords Coords,
	blobKey string,
	sha256sum, sha1sum, md5sum string,
	size int64,
) (*domain.Asset, error) {
	blobStoreName := resolveBlobStore(ctx, d, repo)

	// Resolve UUID for the assets.blob_store_id FK (column type uuid).
	// resolveBlobStore returns a name; look up the actual UUID here.
	blobStoreID := ""
	if bs, err := d.Blobs.Get(ctx, blobStoreName); err == nil && bs != nil {
		blobStoreID = bs.ID
	}

	version := coords.Version
	if version == "" {
		version = "1"
	}
	comp := &domain.Component{
		RepositoryID: repo.ID,
		Format:       string(repo.Format),
		Group:        coords.Group,
		Name:         coords.Name,
		Version:      version,
	}
	if err := d.Components.Create(ctx, comp); err != nil {
		return nil, fmt.Errorf("upsert component: %w", err)
	}

	asset := &domain.Asset{
		ComponentID:  comp.ID,
		RepositoryID: repo.ID,
		Repository:   repo.Name,
		Path:         filePath,
		BlobStoreID:  blobStoreID,
		BlobKey:      blobKey,
		SizeBytes:    size,
		ContentType:  contentType,
		SHA256:       sha256sum,
		SHA1:         sha1sum,
		MD5:          md5sum,
	}
	if err := d.Assets.Create(ctx, asset); err != nil {
		return nil, fmt.Errorf("upsert asset: %w", err)
	}

	_ = d.Blobs.UpdateUsedBytes(ctx, blobStoreName, size)
	return asset, nil
}

// FetchArtifact retrieves a blob from storage and increments download count.
func FetchArtifact(ctx context.Context, d formats.Deps, repoName, filePath string,
) (io.ReadCloser, *domain.Asset, error) {

	asset, err := d.Assets.GetByPath(ctx, repoName, filePath)
	if err != nil {
		return nil, nil, err
	}
	if asset == nil {
		return nil, nil, fmt.Errorf("not found: %s/%s", repoName, filePath)
	}

	rc, _, err := d.BlobStore.Get(ctx, asset.BlobKey)
	if err != nil {
		return nil, nil, fmt.Errorf("blob missing: %w", err)
	}

	go func() { _ = d.Assets.IncrementDownload(ctx, asset.ID) }()
	return rc, asset, nil
}

// DeleteArtifact removes a blob from storage and DB.
func DeleteArtifact(ctx context.Context, d formats.Deps, repoName, filePath string) error {
	asset, err := d.Assets.GetByPath(ctx, repoName, filePath)
	if err != nil {
		return err
	}
	if asset == nil {
		return nil // idempotent
	}
	_ = d.BlobStore.Delete(ctx, asset.BlobKey)
	return d.Assets.Delete(ctx, asset.ID)
}

// BlobKey returns a deterministic content-addressed storage key for a path.
func BlobKey(repoName, filePath string) string {
	h := sha256.Sum256([]byte(repoName + ":" + filePath))
	return hex.EncodeToString(h[:])
}

// BlobKeyByDigest returns a key directly from a sha256 digest string (e.g. "sha256:abc123").
func BlobKeyByDigest(digest string) string {
	return "digest/" + digest
}

// Coords holds the parsed artifact coordinates used for component records.
type Coords struct {
	Group   string // e.g. Maven groupId, npm scope, Go module path
	Name    string // package/artifact/chart name
	Version string // semantic version
}

func resolveBlobStore(ctx context.Context, d formats.Deps, repo *domain.Repository) string {
	if repo.BlobStoreID != nil {
		if bs, err := d.Blobs.Get(ctx, *repo.BlobStoreID); err == nil && bs != nil {
			return bs.Name
		}
	}
	return "default"
}
