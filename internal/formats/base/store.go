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
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/requestctx"
	"github.com/nexspence-oss/nexspence/internal/storage"
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

	// Resolve once — result passed to RegisterStoredBlob to avoid double-call.
	// For group stores, double-call would advance the round-robin counter twice.
	resolvedBlobStoreID, resolvedBlobStoreName, _ := resolveBlobStoreRef(ctx, d, repo)

	var physStore storage.BlobStore
	if resolvedBlobStoreID != "" {
		if bsMeta, getErr := d.Blobs.GetByID(ctx, resolvedBlobStoreID); getErr == nil {
			physStore = PhysicalStore(ctx, d, bsMeta)
		}
	}
	if physStore == nil {
		physStore = d.BlobStore
	}

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

	if err := physStore.Put(ctx, blobKey, pr, declaredSize); err != nil {
		return nil, fmt.Errorf("store blob: %w", err)
	}

	sha256sum := hex.EncodeToString(sha256h.Sum(nil))
	sha1sum   := hex.EncodeToString(sha1h.Sum(nil))
	md5sum    := hex.EncodeToString(md5h.Sum(nil))

	size := declaredSize
	if size <= 0 {
		if s, err := physStore.Size(ctx, blobKey); err == nil {
			size = s
		}
	}

	// Post-write quota check covers streaming uploads where size wasn't declared.
	if size > 0 && declaredSize <= 0 {
		if err := checkQuota(ctx, d, repo, size); err != nil {
			_ = physStore.Delete(ctx, blobKey)
			return nil, err
		}
	}

	asset, err := RegisterStoredBlob(ctx, d, repo, filePath, contentType, coords, blobKey, sha256sum, sha1sum, md5sum, size, resolvedBlobStoreID, resolvedBlobStoreName)
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
// blobStoreID and blobStoreName may be pre-resolved by the caller to avoid calling resolveBlobStoreRef
// twice (which would advance a round-robin group counter twice). Pass empty strings to resolve internally.
func RegisterStoredBlob(ctx context.Context, d formats.Deps, repo *domain.Repository,
	filePath, contentType string, coords Coords,
	blobKey string,
	sha256sum, sha1sum, md5sum string,
	size int64,
	blobStoreID, blobStoreName string,
) (*domain.Asset, error) {
	if blobStoreID == "" {
		var err error
		blobStoreID, blobStoreName, err = resolveBlobStoreRef(ctx, d, repo)
		if err != nil {
			return nil, err
		}
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

	var fetchStore storage.BlobStore
	if asset.BlobStoreID != "" {
		if bsMeta, getErr := d.Blobs.GetByID(ctx, asset.BlobStoreID); getErr == nil {
			fetchStore = PhysicalStore(ctx, d, bsMeta)
		}
	}
	if fetchStore == nil {
		fetchStore = d.BlobStore
	}
	rc, _, err := fetchStore.Get(ctx, asset.BlobKey)
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
	var delStore storage.BlobStore
	if asset.BlobStoreID != "" {
		if bsMeta, getErr := d.Blobs.GetByID(ctx, asset.BlobStoreID); getErr == nil {
			delStore = PhysicalStore(ctx, d, bsMeta)
		}
	}
	if delStore == nil {
		delStore = d.BlobStore
	}
	_ = delStore.Delete(ctx, asset.BlobKey)
	if err := d.Assets.Delete(ctx, asset.ID); err != nil {
		return err
	}
	_ = DecrementBlobStoreUsage(ctx, d.Blobs, asset)
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

// DecrementBlobStoreUsage reduces the owning blob store's used_bytes by asset.SizeBytes.
// Symmetric to the UpdateUsedBytes(+size) call in RegisterStoredBlob. Best-effort — callers
// typically ignore the error (same contract as ArtifactsDeleted metric increments).
func DecrementBlobStoreUsage(ctx context.Context, blobs repository.BlobStoreRepo, asset *domain.Asset) error {
	if asset == nil || asset.SizeBytes <= 0 {
		return nil
	}
	name := ""
	if asset.BlobStoreID != "" {
		bs, err := blobs.GetByID(ctx, asset.BlobStoreID)
		if err != nil {
			return err
		}
		if bs != nil {
			name = bs.Name
		}
	}
	if name == "" {
		name = "default"
	}
	return blobs.UpdateUsedBytes(ctx, name, -asset.SizeBytes)
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

// checkQuota verifies that writing `size` bytes won't exceed either the blob store quota or the
// repository-level quota. Returns ErrQuotaExceeded if either is breached.
// For group stores, the blob-store quota check is deferred to resolveBlobStoreRef:
// PickMember returns "" when all members are at capacity.
func checkQuota(ctx context.Context, d formats.Deps, repo *domain.Repository, size int64) error {
	bs, err := resolveBlobStoreObj(ctx, d, repo)
	if err != nil {
		return err
	}
	if bs.Type != "group" && bs.QuotaBytes != nil && bs.UsedBytes+size > *bs.QuotaBytes {
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

// ResolveBlobStore returns the physical BlobStore, its DB id, and its DB name for repo.
// It mirrors the blob store resolution used inside StoreArtifact, so callers that need to
// write blobs directly (e.g. proxy cache) use the same store that RegisterStoredBlob records.
func ResolveBlobStore(ctx context.Context, d formats.Deps, repo *domain.Repository) (id, name string, store storage.BlobStore) {
	id, name, _ = resolveBlobStoreRef(ctx, d, repo)
	if id != "" {
		if bsMeta, err := d.Blobs.GetByID(ctx, id); err == nil {
			store = PhysicalStore(ctx, d, bsMeta)
		}
	}
	if store == nil {
		store = d.BlobStore
	}
	return id, name, store
}

// PhysicalStore returns the physical BlobStore for the given domain blob store.
// If the registry is set and the descriptor is valid, it returns the cached/created instance.
// Falls back to d.BlobStore (the global default) on any error or missing registry.
func PhysicalStore(ctx context.Context, d formats.Deps, bs *domain.BlobStore) storage.BlobStore {
	if d.Registry == nil || bs == nil {
		return d.BlobStore
	}
	store, err := d.Registry.Get(ctx, storage.BlobStoreDescriptor{
		ID:     bs.ID,
		Type:   bs.Type,
		Config: bs.Config,
	})
	if err != nil {
		return d.BlobStore
	}
	return store
}

// groupMemberIDs extracts member_ids from a group blob store config.
// Handles []string (from Go) and []interface{} (from JSON unmarshal).
func groupMemberIDs(bs *domain.BlobStore) []string {
	if bs.Config == nil {
		return nil
	}
	raw := bs.Config["member_ids"]
	switch v := raw.(type) {
	case []string:
		return v
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// groupFillPolicy returns the fill_policy from a group blob store config, defaulting to "round_robin".
func groupFillPolicy(bs *domain.BlobStore) string {
	if bs.Config == nil {
		return "round_robin"
	}
	if p, ok := bs.Config["fill_policy"].(string); ok && p != "" {
		return p
	}
	return "round_robin"
}

// resolveBlobStoreRef returns the blob store UUID for assets.blob_store_id (FK)
// and the store name for BlobStoreRepo.UpdateUsedBytes (keyed by name).
// For group stores, it picks a physical member using the configured fill policy.
func resolveBlobStoreRef(ctx context.Context, d formats.Deps, repo *domain.Repository) (id string, name string, err error) {
	var bs *domain.BlobStore
	if repo.BlobStoreID != nil {
		ref := strings.TrimSpace(*repo.BlobStoreID)
		if ref != "" {
			bs, err = d.Blobs.GetByID(ctx, ref)
			if err != nil {
				return "", "", fmt.Errorf("blob store: %w", err)
			}
			if bs == nil {
				return "", "", fmt.Errorf("blob store id %q not found", ref)
			}
		}
	}
	if bs == nil {
		bs, err = d.Blobs.Get(ctx, "default")
		if err != nil {
			return "", "", fmt.Errorf("blob store: %w", err)
		}
		if bs == nil {
			return "", "", fmt.Errorf("default blob store not found (seed blob_stores or assign repository.blobStoreId)")
		}
	}

	if bs.Type != "group" {
		return bs.ID, bs.Name, nil
	}

	// Group store: pick a physical member via fill policy.
	memberIDs := groupMemberIDs(bs)
	if len(memberIDs) == 0 {
		return "", "", fmt.Errorf("group blob store %q has no members", bs.Name)
	}
	if d.Registry == nil {
		return "", "", fmt.Errorf("group blob store %q requires Registry to be configured", bs.Name)
	}

	memberMap := make(map[string]domain.BlobStore, len(memberIDs))
	var members []storage.MemberInfo
	for _, mid := range memberIDs {
		m, getErr := d.Blobs.GetByID(ctx, mid)
		if getErr != nil || m == nil {
			continue
		}
		members = append(members, storage.MemberInfo{
			ID:         m.ID,
			QuotaBytes: m.QuotaBytes,
			UsedBytes:  m.UsedBytes,
		})
		memberMap[m.ID] = *m
	}
	if len(members) == 0 {
		return "", "", fmt.Errorf("group blob store %q: no valid members found", bs.Name)
	}

	policy := groupFillPolicy(bs)
	memberID := d.Registry.PickMember(bs.ID, policy, members)
	if memberID == "" {
		return "", "", fmt.Errorf("%w: all members of group blob store %q are at capacity", ErrQuotaExceeded, bs.Name)
	}

	m := memberMap[memberID]
	return m.ID, m.Name, nil
}
