package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/logger"
	"github.com/nexspence-oss/nexspence/internal/metrics"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/storage"
	"github.com/robfig/cron/v3"
)

// CleanupService runs cleanup policies — finds stale assets and removes them.
type CleanupService struct {
	policies  repository.CleanupPolicyRepo
	repos     repository.RepositoryRepo
	assets    repository.AssetRepo
	blobs     repository.BlobStoreRepo
	blobStore storage.BlobStore
	log       logger.Logger

	mu              sync.Mutex
	cronScheduler   *cron.Cron
	entryIDs        map[string]cron.EntryID
	defaultSchedule string
}

func NewCleanupService(
	policies repository.CleanupPolicyRepo,
	repos repository.RepositoryRepo,
	assets repository.AssetRepo,
	blobs repository.BlobStoreRepo,
	blobStore storage.BlobStore,
	log logger.Logger,
) *CleanupService {
	return &CleanupService{
		policies:  policies,
		repos:     repos,
		assets:    assets,
		blobs:     blobs,
		blobStore: blobStore,
		log:       log,
		entryIDs:  make(map[string]cron.EntryID),
	}
}

// StartCronScheduler starts cron-based per-policy scheduling. Run as a goroutine.
// Policies with a non-empty schedule_cron field use that expression; others use defaultSchedule.
func (s *CleanupService) StartCronScheduler(ctx context.Context, defaultSchedule string) {
	s.mu.Lock()
	s.defaultSchedule = defaultSchedule
	s.cronScheduler = cron.New()
	s.mu.Unlock()

	policies, err := s.policies.List(ctx)
	if err != nil {
		s.log.Error("cleanup: failed to load policies for scheduler", "err", err)
	} else {
		s.mu.Lock()
		for _, p := range policies {
			if p.Enabled {
				s.addEntryLocked(p)
			}
		}
		s.mu.Unlock()
	}

	s.cronScheduler.Start()
	<-ctx.Done()
	s.cronScheduler.Stop()
}

// ReloadPolicy updates the cron schedule for a single policy (call after Create/Update/Delete).
// If the policy is not found or disabled, its cron entry is removed.
func (s *CleanupService) ReloadPolicy(ctx context.Context, policyID string) {
	// Fetch from DB outside the lock to avoid holding it during I/O.
	p, _ := s.policies.Get(ctx, policyID)

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cronScheduler == nil {
		return // scheduler not started yet
	}

	// Remove existing entry if present.
	if eid, ok := s.entryIDs[policyID]; ok {
		s.cronScheduler.Remove(eid)
		delete(s.entryIDs, policyID)
	}

	if p == nil || !p.Enabled {
		return
	}
	s.addEntryLocked(*p)
}

// addEntryLocked registers a cron job for policy p. Caller must hold s.mu.
func (s *CleanupService) addEntryLocked(p domain.CleanupPolicy) {
	schedule := p.ScheduleCron
	if schedule == "" {
		schedule = s.defaultSchedule
	}

	job := func() {
		if err := s.runPolicy(context.Background(), p); err != nil {
			s.log.Error("cleanup cron error", "policy", p.Name, "err", err)
		}
	}

	id, err := s.cronScheduler.AddFunc(schedule, job)
	if err != nil {
		s.log.Warn("cleanup: invalid schedule_cron, falling back to default",
			"policy", p.Name, "schedule", schedule, "err", err)
		id, _ = s.cronScheduler.AddFunc(s.defaultSchedule, job)
	}
	s.entryIDs[p.ID] = id
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
	pathPrefix := strCriteria(p.Criteria, "pathPrefix")
	nameGlob := strCriteria(p.Criteria, "nameGlob")

	if lastDownloadedDays == 0 && artifactAgeDays == 0 {
		s.log.Info("cleanup: no criteria set, skipping", "policy", p.Name)
		return nil
	}

	repoNames, err := s.repos.ListNamesByCleanupPolicyID(ctx, p.ID)
	if err != nil {
		return fmt.Errorf("cleanup: list repos for policy: %w", err)
	}
	if len(repoNames) == 0 {
		s.log.Info("cleanup: policy not attached to any repository (set cleanup policies on repositories), skipping", "policy", p.Name)
		return nil
	}

	const batchLimit = 500
	var freed int64
	var deleted int
	for {
		stale, err := s.assets.ListStale(ctx, p.Format, repoNames, lastDownloadedDays, artifactAgeDays, pathPrefix, nameGlob, p.RetainNVersions, batchLimit)
		if err != nil {
			return fmt.Errorf("cleanup: list stale assets: %w", err)
		}
		if len(stale) == 0 {
			break
		}
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
			asset := a
			_ = base.DecrementBlobStoreUsage(ctx, s.blobs, &asset)
			metrics.ArtifactsDeleted.Add(1)
			freed += a.SizeBytes
			deleted++
		}
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

func strCriteria(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
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
