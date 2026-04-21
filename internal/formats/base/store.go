// Package base provides shared artifact storage helpers used by all format handlers.
package base

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats"
	"github.com/nexspence-oss/nexspence/internal/metrics"
	"github.com/nexspence-oss/nexspence/internal/requestctx"
)

// ErrQuotaExceeded is returned by StoreArtifact when a blob store or repository quota would be exceeded.
var ErrQuotaExceeded = errors.New("storage quota exceeded")

// HTTPStatusForError maps known storage errors to appropriate HTTP status codes.
// Returns 507 Insufficient Storage for quota errors, 500 for everything else.
func HTTPStatusForError(err error) int {
	if errors.Is(err, ErrQuotaExceeded) {
		return http.StatusInsufficientStorage
	}
	return http.StatusInternalServerError
}

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

	// Early quota reject when declared size is known.
	if declaredSize > 0 {
		if err := checkQuota(ctx, d, repo, declaredSize); err != nil {
			return nil, err
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

	// Post-write quota check covers streaming uploads where size wasn't declared.
	if size > 0 && declaredSize <= 0 {
		if err := checkQuota(ctx, d, repo, size); err != nil {
			_ = d.BlobStore.Delete(ctx, blobKey)
			return nil, err
		}
	}

	asset, err := RegisterStoredBlob(ctx, d, repo, filePath, contentType, coords, blobKey, sha256sum, sha1sum, md5sum, size)
	if err != nil {
		return nil, err
	}

	if d.Webhooks != nil {
		d.Webhooks.Dispatch(domain.WebhookPayload{
			Event:      domain.EventArtifactPublished,
			Timestamp:  asset.CreatedAt,
			Repository: repoName,
			Component: map[string]any{
				"group":   coords.Group,
				"name":    coords.Name,
				"version": coords.Version,
				"format":  string(repo.Format),
			},
			Asset: map[string]any{
				"path":        filePath,
				"contentType": contentType,
				"size":        size,
			},
		})
	}

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
	blobStoreID, blobStoreName, err := resolveBlobStoreRef(ctx, d, repo)
	if err != nil {
		return nil, err
	}

	version := coords.Version
	if version == "" {
		version = "1"
	}
	comp := &domain.Component{
		RepositoryID: repo.ID,
		Repository:   repo.Name,
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
	if uid := requestctx.UserID(ctx); uid != "" {
		asset.UploaderID = uid
	}
	if err := d.Assets.Create(ctx, asset); err != nil {
		return nil, fmt.Errorf("upsert asset: %w", err)
	}

	_ = d.Blobs.UpdateUsedBytes(ctx, blobStoreName, size)
	metrics.ArtifactsStored.Add(1)
	metrics.BytesStored.Add(size)
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

	go func(assetID string) {
		_ = d.Assets.IncrementDownload(context.Background(), assetID)
	}(asset.ID)
	metrics.DownloadsTotal.Add(1)
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
	if err := d.Assets.Delete(ctx, asset.ID); err != nil {
		return err
	}
	metrics.ArtifactsDeleted.Add(1)
	if d.Webhooks != nil {
		d.Webhooks.Dispatch(domain.WebhookPayload{
			Event:      domain.EventArtifactDeleted,
			Timestamp:  asset.LastModified,
			Repository: repoName,
			Asset: map[string]any{
				"path":        filePath,
				"contentType": asset.ContentType,
				"size":        asset.SizeBytes,
			},
		})
	}
	return nil
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

// checkQuota verifies that writing `size` bytes won't exceed either the blob store
// quota or the repository-level quota. Returns ErrQuotaExceeded if either is breached.
func checkQuota(ctx context.Context, d formats.Deps, repo *domain.Repository, size int64) error {
	bs, err := resolveBlobStoreObj(ctx, d, repo)
	if err != nil {
		return err
	}
	if bs.QuotaBytes != nil && bs.UsedBytes+size > *bs.QuotaBytes {
		return fmt.Errorf("%w: blob store %q usage %d + %d > limit %d",
			ErrQuotaExceeded, bs.Name, bs.UsedBytes, size, *bs.QuotaBytes)
	}
	if repo.QuotaBytes != nil {
		used, err := d.Assets.SumSizeByRepo(ctx, repo.Name)
		if err != nil {
			return fmt.Errorf("quota check: %w", err)
		}
		if used+size > *repo.QuotaBytes {
			return fmt.Errorf("%w: repository %q usage %d + %d > limit %d",
				ErrQuotaExceeded, repo.Name, used, size, *repo.QuotaBytes)
		}
	}
	return nil
}

// resolveBlobStoreObj returns the full BlobStore record for a repository.
func resolveBlobStoreObj(ctx context.Context, d formats.Deps, repo *domain.Repository) (*domain.BlobStore, error) {
	if repo.BlobStoreID != nil {
		ref := strings.TrimSpace(*repo.BlobStoreID)
		if ref != "" {
			bs, err := d.Blobs.GetByID(ctx, ref)
			if err != nil {
				return nil, fmt.Errorf("blob store: %w", err)
			}
			if bs != nil {
				return bs, nil
			}
			return nil, fmt.Errorf("blob store id %q not found", ref)
		}
	}
	bs, err := d.Blobs.Get(ctx, "default")
	if err != nil {
		return nil, fmt.Errorf("blob store: %w", err)
	}
	if bs == nil {
		return nil, fmt.Errorf("default blob store not found")
	}
	return bs, nil
}

// resolveBlobStoreRef returns the blob store UUID for assets.blob_store_id (FK)
// and the store name for BlobStoreRepo.UpdateUsedBytes (keyed by name).
func resolveBlobStoreRef(ctx context.Context, d formats.Deps, repo *domain.Repository) (id string, name string, err error) {
	if repo.BlobStoreID != nil {
		ref := strings.TrimSpace(*repo.BlobStoreID)
		if ref != "" {
			bs, err := d.Blobs.GetByID(ctx, ref)
			if err != nil {
				return "", "", fmt.Errorf("blob store: %w", err)
			}
			if bs != nil {
				return bs.ID, bs.Name, nil
			}
			return "", "", fmt.Errorf("blob store id %q not found", ref)
		}
	}
	bs, err := d.Blobs.Get(ctx, "default")
	if err != nil {
		return "", "", fmt.Errorf("blob store: %w", err)
	}
	if bs == nil {
		return "", "", fmt.Errorf("default blob store not found (seed blob_stores or assign repository.blobStoreId)")
	}
	return bs.ID, bs.Name, nil
}
