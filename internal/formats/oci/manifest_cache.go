package oci

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/storage"
)

func (h *Handler) cachedManifest(ctx context.Context, repoName, p string, strictType bool) (*domain.Asset, *manifestBody, error) {
	asset, err := h.deps.Assets.GetByPath(ctx, repoName, p)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("lookup manifest: %w", err)
	}
	store, err := base.AssetPhysicalStore(ctx, h.deps, asset)
	if err != nil {
		return nil, nil, err
	}
	rc, _, err := store.Get(ctx, asset.BlobKey)
	if err != nil {
		return nil, nil, nil
	} // missing blob: repair from upstream
	defer func() { _ = rc.Close() }()
	body, err := spoolManifest(rc)
	if err != nil {
		return nil, nil, nil
	}
	ct, err := body.contentType(asset.ContentType)
	if err != nil && !strictType {
		// Hosted manifests written without Content-Type historically defaulted to
		// Docker even for OCI bodies. Recover the actual type without changing bytes.
		ct, err = body.contentType("")
	}
	sum := body.sha256
	if err != nil || (asset.SHA256 != "" && sum != asset.SHA256) || (strings.Contains(p[strings.LastIndex(p, "/")+1:], ":") && !strings.HasSuffix(p, "/sha256:"+sum)) {
		body.Close()
		return nil, nil, nil
	}
	verified := *asset
	verified.SHA256, verified.ContentType, verified.SizeBytes = sum, ct, body.size
	return &verified, body, nil
}

func (h *Handler) persistManifest(ctx context.Context, repo *domain.Repository, image, ref, ct string, body *manifestBody, pin *domain.Asset) (*domain.Asset, error) {
	sum := body.sha256
	digestRef := "sha256:" + sum
	fp := manifestPath(image, digestRef)
	key := base.BlobKey(repo.Name, fp)
	legacyCopy := pin != nil
	var result *domain.Asset
	err := h.deps.Assets.WithBlobKeyLock(ctx, key, func(ctx context.Context) error {
		alias, err := h.deps.Assets.GetByPath(ctx, repo.Name, fp)
		if err != nil && !errors.Is(err, repository.ErrNotFound) {
			return fmt.Errorf("lookup digest alias: %w", err)
		}
		if alias != nil && alias.BlobKey == key && alias.SHA256 == sum {
			reusable, checkErr := h.manifestAliasReusable(ctx, alias, pin, body)
			if checkErr != nil {
				return checkErr
			}
			if !reusable {
				// Repair on the registered store, even if a group now picks another.
				if pin == nil {
					pin = alias
				}
				alias = nil
			} else if alias.ContentType != ct {
				alias, err = base.RegisterStoredBlob(ctx, h.deps, repo, fp, ct, base.Coords{Name: image, Version: digestRef}, key, sum, alias.SHA1, alias.MD5, body.size, alias.BlobStoreID, "")
				if err != nil {
					return err
				}
				h.typeManifest(ctx, alias, body)
			}
		} else {
			alias = nil
		}

		if alias == nil {
			// Pin legacy copies to their original physical store, including groups.
			storeID, storeName, store, serr := h.manifestStore(ctx, repo, pin)
			if serr != nil {
				return serr
			}
			// A missing physical object with registered references is already
			// charged to quota. Replacing it adds no bytes to the accounting.
			count, countErr := h.deps.Assets.CountByBlobKeyInStore(ctx, key, storeID)
			if countErr != nil {
				return fmt.Errorf("check manifest references: %w", countErr)
			}
			if count == 0 {
				if qerr := base.CheckQuota(ctx, h.deps, repo, body.size); qerr != nil {
					return qerr
				}
			}
			if err := store.Put(ctx, key, body.reader(), body.size); err != nil {
				return fmt.Errorf("store manifest: %w", err)
			}
			alias, err = base.RegisterStoredBlob(ctx, h.deps, repo, fp, ct, base.Coords{Name: image, Version: digestRef}, key, sum,
				body.sha1, body.md5, body.size, storeID, storeName)
			if err != nil {
				if count, cerr := h.deps.Assets.CountByBlobKey(ctx, key, ""); cerr == nil && count == 0 {
					_ = store.Delete(ctx, key)
				}
				return err
			}
			h.typeManifest(ctx, alias, body)
		}
		result = alias
		if ref != digestRef && !legacyCopy {
			result, err = base.RegisterStoredBlob(ctx, h.deps, repo, manifestPath(image, ref), ct, base.Coords{Name: image, Version: ref}, alias.BlobKey, sum, alias.SHA1, alias.MD5, body.size, alias.BlobStoreID, "")
			if err != nil {
				return err
			}
			h.typeManifest(ctx, result, body)
		}
		return nil
	})
	return result, err
}

func (h *Handler) typeManifest(ctx context.Context, asset *domain.Asset, body *manifestBody) {
	extra := extraFrom(body.meta)
	extra[extraSourceDigestKey] = asset.SHA256
	_ = h.deps.Components.UpdateExtra(ctx, asset.ComponentID, extra)
}

func (h *Handler) manifestStore(ctx context.Context, repo *domain.Repository, pin *domain.Asset) (string, string, storage.BlobStore, error) {
	if pin == nil {
		return base.ResolveBlobStore(ctx, h.deps, repo)
	}
	store, err := base.AssetPhysicalStore(ctx, h.deps, pin)
	return pin.BlobStoreID, "", store, err
}

func (h *Handler) manifestAliasReusable(ctx context.Context, alias, pin *domain.Asset, body *manifestBody) (bool, error) {
	store, err := base.AssetPhysicalStore(ctx, h.deps, alias)
	if err != nil {
		return false, err
	}
	exists, err := store.Exists(ctx, alias.BlobKey)
	if err != nil {
		return false, fmt.Errorf("check digest blob: %w", err)
	}
	if !exists {
		return false, nil
	}
	// A verified tag that already points at the immutable object proves its
	// bytes too. Legacy detached aliases and fresh upstream fills need a read.
	if pin != nil && pin.BlobKey == alias.BlobKey && pin.BlobStoreID == alias.BlobStoreID && pin.SHA256 == alias.SHA256 {
		return true, nil
	}
	rc, _, err := store.Get(ctx, alias.BlobKey)
	if err != nil {
		return false, nil
	}
	defer func() { _ = rc.Close() }()
	held, err := spoolManifest(rc)
	if err != nil {
		return false, nil
	}
	defer held.Close()
	return held.sha256 == body.sha256 && held.size == body.size, nil
}
