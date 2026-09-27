// Package service contains business logic for backup and restore of all repository data.
package service

import (
	"context"
	"errors"
	"fmt"

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
	// BlobStore is the store used when Resolver is nil — kept for
	// backward-compat callers, but every asset read/write should go through
	// resolveStore so a non-default blob store (S3/Azure, or any repo not
	// pinned to the instance default) round-trips through its real bytes.
	BlobStore storage.BlobStore
	// Resolver resolves an asset's actual physical blob store by ID, the same
	// mechanism CleanupService/GCService/ReplicationService already use
	// (internal/api/router.go's blobRegistry). Without it, Export/Restore/
	// ImportRepo use BlobStore for every asset regardless of where it really
	// lives — see resolveStore.
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

// resolveStore resolves the physical blob store for blobStoreID via Resolver.
// With no resolver wired, or no id, it returns BlobStore: a single-store setup,
// where that is the only store there is. Otherwise a store it cannot resolve
// is an error, never a fallback to BlobStore — a read would then look in the
// wrong store (a backup silently missing every blob on it) and a write would
// put bytes where the asset row does not say they are (#490 review). A group
// is an error too: it holds no bytes of its own, its members do.
//
// cache (may be nil) memoises successful resolutions for the length of one
// Export/Restore/ImportRepo call, so each store costs one blob_stores lookup
// per operation instead of one per asset. Failures are not cached: the next
// asset on the same store retries rather than inheriting a transient error.
func (s *BackupService) resolveStore(ctx context.Context, cache storeCache, blobStoreID string) (storage.BlobStore, error) {
	if s.Resolver == nil || blobStoreID == "" {
		return s.BlobStore, nil
	}
	if store, ok := cache[blobStoreID]; ok {
		return store, nil
	}
	bs, err := s.BlobStores.GetByID(ctx, blobStoreID)
	if err != nil {
		return nil, fmt.Errorf("blob store %s: %w", blobStoreID, err)
	}
	if bs == nil {
		return nil, fmt.Errorf("blob store %s not found", blobStoreID)
	}
	if bs.Type == "group" {
		return nil, fmt.Errorf("blob store %s (%s) is a group, not a physical store", bs.Name, bs.ID)
	}
	store, err := s.Resolver.Get(ctx, storage.BlobStoreDescriptor{ID: bs.ID, Type: bs.Type, Config: bs.Config})
	if err == nil && store == nil {
		err = errors.New("resolver returned no store")
	}
	if err != nil {
		return nil, fmt.Errorf("resolve blob store %s (%s): %w", bs.Name, bs.ID, err)
	}
	if cache != nil {
		cache[blobStoreID] = store
	}
	return store, nil
}

// storeCache is resolveStore's per-operation memo: blob store ID → physical store.
type storeCache map[string]storage.BlobStore

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
	// BlobsFailed counts blobs the archive carried but that could not be
	// written; their assets are not restored (see putArchivedBlob).
	BlobsFailed int `json:"blobsFailed"`
	FailureReport
}

// maxReportedFailures caps FailureReport.Failures: a restore that fails on
// every asset of a big repository must not answer with a megabyte of errors.
// FailedItems still counts them all, and every one is logged.
const maxReportedFailures = 100

// RestoreFailure names one archived item that a restore or import could not
// bring back.
type RestoreFailure struct {
	Kind  string `json:"kind"` // blobStore | repository | user | role | cleanupPolicy | component | asset
	Name  string `json:"name"`
	Error string `json:"error"`
}

// FailureReport lists what a restore or import had to leave out, so a partial
// result is not reported as a complete one (#551). Items skipped only because
// their parent failed (the components and assets of a repository that could
// not be created) are covered by the parent's entry, not listed one by one.
type FailureReport struct {
	FailedItems int              `json:"failedItems"`
	Failures    []RestoreFailure `json:"failures,omitempty"`
}

func (r *FailureReport) add(kind, name string, err error) {
	r.FailedItems++
	if len(r.Failures) < maxReportedFailures {
		r.Failures = append(r.Failures, RestoreFailure{Kind: kind, Name: name, Error: err.Error()})
	}
}

// recordFailure logs a skipped item and adds it to report.
func (s *BackupService) recordFailure(report *FailureReport, op, kind, name string, err error) {
	s.logWarn(op+": item not restored", "kind", kind, "name", name, "err", err)
	report.add(kind, name, err)
}

// backupUser carries the password hash in backup archives (json:"-" hides it in normal API responses).
type backupUser struct {
	domain.User
	PasswordHash string `json:"passwordHash"`
}
