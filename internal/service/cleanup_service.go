package service

import (
	"context"
	"fmt"
	"time"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/logger"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/storage"
)

// CleanupService runs cleanup policies — finds stale assets and removes them.
type CleanupService struct {
	policies  repository.CleanupPolicyRepo
	assets    repository.AssetRepo
	blobStore storage.BlobStore
	log       logger.Logger
}

func NewCleanupService(
	policies repository.CleanupPolicyRepo,
	assets repository.AssetRepo,
	blobStore storage.BlobStore,
	log logger.Logger,
) *CleanupService {
	return &CleanupService{policies: policies, assets: assets, blobStore: blobStore, log: log}
}

// RunAll executes all enabled cleanup policies once and returns a summary.
func (s *CleanupService) RunAll(ctx context.Context) error {
	policies, err := s.policies.List(ctx)
	if err != nil {
		return fmt.Errorf("cleanup: list policies: %w", err)
	}
	for _, p := range policies {
		if !p.Enabled {
			continue
		}
		if err := s.runPolicy(ctx, p); err != nil {
			s.log.Error("cleanup policy failed", "policy", p.Name, "err", err)
		}
	}
	return nil
}

// RunPolicy executes a single policy by ID.
func (s *CleanupService) RunPolicy(ctx context.Context, id string) error {
	p, err := s.policies.Get(ctx, id)
	if err != nil {
		return err
	}
	if p == nil {
		return fmt.Errorf("cleanup policy %q not found", id)
	}
	return s.runPolicy(ctx, *p)
}

func (s *CleanupService) runPolicy(ctx context.Context, p domain.CleanupPolicy) error {
	lastDownloadedDays := intCriteria(p.Criteria, "lastDownloadedDays")
	artifactAgeDays := intCriteria(p.Criteria, "artifactAgeDays")

	if lastDownloadedDays == 0 && artifactAgeDays == 0 {
		s.log.Info("cleanup: no criteria set, skipping", "policy", p.Name)
		return nil
	}

	stale, err := s.assets.ListStale(ctx, p.Format, lastDownloadedDays, artifactAgeDays, 1000)
	if err != nil {
		return fmt.Errorf("cleanup: list stale assets: %w", err)
	}

	var freed int64
	var deleted int
	for _, a := range stale {
		if p.DryRun {
			s.log.Info("cleanup dry-run: would delete", "policy", p.Name,
				"asset", a.Path, "repo", a.Repository, "size", a.SizeBytes)
			freed += a.SizeBytes
			deleted++
			continue
		}
		if err := s.blobStore.Delete(ctx, a.BlobKey); err != nil {
			s.log.Warn("cleanup: blob delete failed", "key", a.BlobKey, "err", err)
		}
		if err := s.assets.Delete(ctx, a.ID); err != nil {
			s.log.Warn("cleanup: asset delete failed", "id", a.ID, "err", err)
			continue
		}
		freed += a.SizeBytes
		deleted++
	}

	now := time.Now()
	p.LastRunAt = &now
	p.LastRunFreed = freed
	p.LastRunCount = deleted
	if err := s.policies.Update(ctx, &p); err != nil {
		s.log.Warn("cleanup: failed to update policy stats", "policy", p.Name, "err", err)
	}

	s.log.Info("cleanup policy complete",
		"policy", p.Name,
		"deleted", deleted,
		"freed_bytes", freed,
		"dry_run", p.DryRun)
	return nil
}

// StartScheduler runs cleanup policies in background on a fixed interval.
// Call it as a goroutine: go svc.StartScheduler(ctx).
func (s *CleanupService) StartScheduler(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.RunAll(ctx); err != nil {
				s.log.Error("cleanup scheduler error", "err", err)
			}
		}
	}
}

func intCriteria(m map[string]any, key string) int {
	if m == nil {
		return 0
	}
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	case int64:
		return int(n)
	}
	return 0
}
