// Package service contains business logic for backup and restore of all repository data.
package service

import (
	"context"
	"errors"

	"github.com/nexspence-oss/nexspence/internal/distlock"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/logger"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/storage"
)

// BackupService exports and restores all repository data (metadata + blobs).
type BackupService struct {
	BlobStores repository.BlobStoreRepo
	Repos      repository.RepositoryRepo
	Users      repository.UserRepo
	Roles      repository.RoleRepo
	Policies   repository.CleanupPolicyRepo
	Components repository.ComponentRepo
	Assets     repository.AssetRepo
	// BlobStore is the fallback used when Resolver is nil or a lookup fails —
	// kept for backward-compat callers, but every asset read/write should go
	// through storeFor so a non-default blob store (S3/Azure, or any repo not
	// pinned to the instance default) round-trips through its real bytes.
	BlobStore storage.BlobStore
	// Resolver resolves an asset's actual physical blob store by ID, the same
	// mechanism CleanupService/GCService/ReplicationService already use
	// (internal/api/router.go's blobRegistry). Without it, Export/Restore/
	// ImportRepo silently fall back to BlobStore for every asset regardless
	// of where it really lives — see storeFor.
	Resolver StoreResolver

	// Settings backs the scheduled-backup feature (spec 37) — set via
	// WithSettings. Nil means scheduling is unused; RunScheduled/
	// ReloadSchedule are then no-ops rather than panicking, so a caller that
	// only wants manual Export/Restore never has to wire it.
	Settings repository.BackupSettingsRepo
	locker   distlock.Locker
	log      logger.Logger
	audit    repository.AuditRepo
	sched    scheduledBackupState
}

// storeFor resolves the physical blob store for blobStoreID via Resolver,
// falling back to BlobStore when no resolver is wired, the id is empty, or
// resolution fails. Centralizes the fallback so every call site — export's
// read path, restore/import's write path — treats a store it cannot resolve
// the same way, instead of three independently-written fallbacks drifting
// out of sync with each other over time.
func (s *BackupService) storeFor(ctx context.Context, blobStoreID string) storage.BlobStore {
	if s.Resolver == nil || blobStoreID == "" {
		return s.BlobStore
	}
	bs, err := s.BlobStores.GetByID(ctx, blobStoreID)
	if err != nil || bs == nil {
		return s.BlobStore
	}
	store, err := s.Resolver.Get(ctx, storage.BlobStoreDescriptor{ID: bs.ID, Type: bs.Type, Config: bs.Config})
	if err != nil || store == nil {
		return s.BlobStore
	}
	return store
}

// Sentinel errors for per-repository operations.
var (
	ErrRepoNotFound = errors.New("repository not found")
	ErrRepoConflict = errors.New("repository already exists")
)

// RestoreStats reports what was restored.
type RestoreStats struct {
	BlobStores int `json:"blobStores"`
	Repos      int `json:"repositories"`
	Users      int `json:"users"`
	Roles      int `json:"roles"`
	Policies   int `json:"cleanupPolicies"`
	Components int `json:"components"`
	Assets     int `json:"assets"`
	Blobs      int `json:"blobs"`
}

// backupUser carries the password hash in backup archives (json:"-" hides it in normal API responses).
type backupUser struct {
	domain.User
	PasswordHash string `json:"passwordHash"`
}
