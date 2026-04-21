package service

import (
	"context"
	"fmt"

	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/storage"
)

// GCResult reports what was found and removed during a compaction run.
type GCResult struct {
	ScannedBlobs int    `json:"scannedBlobs"`
	Orphans      int    `json:"orphans"`
	FreedBytes   int64  `json:"freedBytes"`
	DryRun       bool   `json:"dryRun"`
	Errors       []string `json:"errors,omitempty"`
}

// BlobGCService finds and removes blobs in a blob store that are not
// referenced by any asset record (orphaned blobs).
type BlobGCService struct {
	Assets    repository.AssetRepo
	BlobStore storage.BlobStore
}

// Compact scans all keys in the blob store, checks each against the asset DB,
// and deletes any key that has no corresponding asset.
// If dryRun is true, orphans are reported but not deleted.
func (s *BlobGCService) Compact(ctx context.Context, dryRun bool) (*GCResult, error) {
	// Build set of all referenced blob keys from the DB.
	dbKeys, err := s.Assets.ListAllBlobKeys(ctx)
	if err != nil {
		return nil, fmt.Errorf("list db blob keys: %w", err)
	}
	referenced := make(map[string]struct{}, len(dbKeys))
	for _, k := range dbKeys {
		referenced[k] = struct{}{}
	}

	// List all keys present in the physical store.
	storeKeys, err := s.BlobStore.ListKeys(ctx)
	if err != nil {
		return nil, fmt.Errorf("list store keys: %w", err)
	}

	result := &GCResult{ScannedBlobs: len(storeKeys), DryRun: dryRun}

	for _, key := range storeKeys {
		if _, ok := referenced[key]; ok {
			continue // still used
		}
		// Orphan found.
		size, _ := s.BlobStore.Size(ctx, key)
		result.Orphans++
		result.FreedBytes += size

		if !dryRun {
			if err := s.BlobStore.Delete(ctx, key); err != nil {
				result.Errors = append(result.Errors,
					fmt.Sprintf("delete %s: %v", key, err))
			}
		}
	}

	return result, nil
}
