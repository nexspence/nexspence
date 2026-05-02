package service

import (
	"context"
	"fmt"
	"sync"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/storage"
)

// BlobStoreMigrationService manages background migrations of repository blobs
// from one blob store to another.
type BlobStoreMigrationService struct {
	migrations repository.BlobStoreMigrationRepo
	assets     repository.AssetRepo
	repos      repository.RepositoryRepo
	blobs      repository.BlobStoreRepo
	registry   *storage.Registry

	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

func NewBlobStoreMigrationService(
	migrations repository.BlobStoreMigrationRepo,
	assets repository.AssetRepo,
	repos repository.RepositoryRepo,
	blobs repository.BlobStoreRepo,
	registry *storage.Registry,
) *BlobStoreMigrationService {
	return &BlobStoreMigrationService{
		migrations: migrations,
		assets:     assets,
		repos:      repos,
		blobs:      blobs,
		registry:   registry,
		cancels:    make(map[string]context.CancelFunc),
	}
}

// Start validates inputs, creates a migration record, and launches the background goroutine.
func (s *BlobStoreMigrationService) Start(ctx context.Context, repoName, targetStoreID string) (*domain.BlobStoreMigration, error) {
	repo, err := s.repos.Get(ctx, repoName)
	if err != nil {
		return nil, fmt.Errorf("get repo: %w", err)
	}
	if repo == nil {
		return nil, fmt.Errorf("repository %q not found", repoName)
	}

	// Validate target store exists.
	targetStore, err := s.blobs.GetByID(ctx, targetStoreID)
	if err != nil {
		return nil, fmt.Errorf("get target store: %w", err)
	}
	if targetStore == nil {
		return nil, fmt.Errorf("target blob store not found")
	}

	// Validate: not the same as current.
	if repo.BlobStoreID != nil && *repo.BlobStoreID == targetStoreID {
		return nil, fmt.Errorf("target blob store is the same as the repository's current store")
	}

	// Enforce single active migration per repo.
	active, err := s.migrations.GetActiveByRepo(ctx, repoName)
	if err != nil {
		return nil, err
	}
	if active != nil {
		return nil, fmt.Errorf("a migration is already running for this repository")
	}

	// Capture source store ID for the history record.
	sourceStoreID := ""
	if repo.BlobStoreID != nil {
		sourceStoreID = *repo.BlobStoreID
	}

	m := &domain.BlobStoreMigration{
		RepositoryName: repoName,
		SourceStoreID:  sourceStoreID,
		TargetStoreID:  targetStoreID,
		Status:         "pending",
	}
	if err := s.migrations.Create(ctx, m); err != nil {
		return nil, fmt.Errorf("create migration record: %w", err)
	}

	migCtx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.cancels[m.ID] = cancel
	s.mu.Unlock()

	go s.runMigration(migCtx, m)
	return m, nil
}

// Cancel signals the running migration goroutine to stop.
func (s *BlobStoreMigrationService) Cancel(ctx context.Context, migrationID string) error {
	s.mu.Lock()
	cancel, ok := s.cancels[migrationID]
	s.mu.Unlock()
	if ok {
		cancel()
	}
	return nil
}

// GetLatestByRepo returns the most recent migration for a repo regardless of status.
func (s *BlobStoreMigrationService) GetLatestByRepo(ctx context.Context, repoName string) (*domain.BlobStoreMigration, error) {
	return s.migrations.GetLatestByRepo(ctx, repoName)
}

// ResumeAll is called on server startup to mark interrupted migrations as cancelled
// so users can restart them. Goroutines cannot be safely resumed across process restarts.
func (s *BlobStoreMigrationService) ResumeAll(ctx context.Context) error {
	active, err := s.migrations.ListActive(ctx)
	if err != nil {
		return err
	}
	interrupted := "interrupted by server restart"
	for _, m := range active {
		_ = s.migrations.FinishMigration(ctx, m.ID, "cancelled", &interrupted)
	}
	return nil
}

func (s *BlobStoreMigrationService) runMigration(ctx context.Context, m *domain.BlobStoreMigration) {
	defer func() {
		s.mu.Lock()
		delete(s.cancels, m.ID)
		s.mu.Unlock()
	}()

	bgCtx := context.Background()

	if err := s.migrations.UpdateStatus(bgCtx, m.ID, "running", nil); err != nil {
		return
	}

	rows, err := s.assets.ListForBlobStoreMigration(bgCtx, m.RepositoryName, m.TargetStoreID)
	if err != nil {
		errMsg := err.Error()
		_ = s.migrations.FinishMigration(bgCtx, m.ID, "failed", &errMsg)
		return
	}

	var totalBytes int64
	for _, r := range rows {
		totalBytes += r.SizeBytes
	}
	_ = s.migrations.SetTotals(bgCtx, m.ID, len(rows), totalBytes)

	// Load target store descriptor once.
	targetStoreMeta, err := s.blobs.GetByID(bgCtx, m.TargetStoreID)
	if err != nil || targetStoreMeta == nil {
		errMsg := fmt.Sprintf("cannot load target store: %v", err)
		_ = s.migrations.FinishMigration(bgCtx, m.ID, "failed", &errMsg)
		return
	}
	targetStore, err := s.registry.Get(bgCtx, storage.BlobStoreDescriptor{
		ID:     targetStoreMeta.ID,
		Type:   targetStoreMeta.Type,
		Config: targetStoreMeta.Config,
	})
	if err != nil {
		errMsg := fmt.Sprintf("cannot open target store: %v", err)
		_ = s.migrations.FinishMigration(bgCtx, m.ID, "failed", &errMsg)
		return
	}

	doneAssets := 0
	var doneBytes int64

	for _, row := range rows {
		select {
		case <-ctx.Done():
			_ = s.migrations.FinishMigration(bgCtx, m.ID, "cancelled", nil)
			return
		default:
		}

		// Load source store for this blob.
		sourceMeta, err := s.blobs.GetByID(bgCtx, row.SourceBlobStoreID)
		if err != nil || sourceMeta == nil {
			errMsg := fmt.Sprintf("cannot load source store %s: %v", row.SourceBlobStoreID, err)
			_ = s.migrations.FinishMigration(bgCtx, m.ID, "failed", &errMsg)
			return
		}
		sourceStore, err := s.registry.Get(bgCtx, storage.BlobStoreDescriptor{
			ID:     sourceMeta.ID,
			Type:   sourceMeta.Type,
			Config: sourceMeta.Config,
		})
		if err != nil {
			errMsg := fmt.Sprintf("cannot open source store: %v", err)
			_ = s.migrations.FinishMigration(bgCtx, m.ID, "failed", &errMsg)
			return
		}

		// Copy blob if not already in target (resume support).
		exists, err := targetStore.Exists(bgCtx, row.BlobKey)
		if err != nil {
			errMsg := fmt.Sprintf("checking target for %s: %v", row.BlobKey, err)
			_ = s.migrations.FinishMigration(bgCtx, m.ID, "failed", &errMsg)
			return
		}
		if !exists {
			rc, size, err := sourceStore.Get(bgCtx, row.BlobKey)
			if err != nil {
				errMsg := fmt.Sprintf("reading blob %s: %v", row.BlobKey, err)
				_ = s.migrations.FinishMigration(bgCtx, m.ID, "failed", &errMsg)
				return
			}
			putErr := targetStore.Put(bgCtx, row.BlobKey, rc, size)
			_ = rc.Close()
			if putErr != nil {
				errMsg := fmt.Sprintf("writing blob %s: %v", row.BlobKey, putErr)
				_ = s.migrations.FinishMigration(bgCtx, m.ID, "failed", &errMsg)
				return
			}
			_ = s.blobs.UpdateUsedBytes(bgCtx, targetStoreMeta.Name, size)
		}

		if err := s.assets.UpdateBlobStoreForBlobKey(bgCtx, row.BlobKey, m.RepositoryName, m.TargetStoreID); err != nil {
			errMsg := fmt.Sprintf("updating asset pointers for %s: %v", row.BlobKey, err)
			_ = s.migrations.FinishMigration(bgCtx, m.ID, "failed", &errMsg)
			return
		}

		doneAssets++
		doneBytes += row.SizeBytes
		_ = s.migrations.UpdateProgress(bgCtx, m.ID, doneAssets, doneBytes)
	}

	// Update repository's blob_store_id to target.
	repo, err := s.repos.Get(bgCtx, m.RepositoryName)
	if err == nil && repo != nil {
		repo.BlobStoreID = &m.TargetStoreID
		_ = s.repos.Update(bgCtx, repo)
	}

	_ = s.migrations.FinishMigration(bgCtx, m.ID, "done", nil)
}
