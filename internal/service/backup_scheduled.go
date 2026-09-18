package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/nexspence-oss/nexspence/internal/distlock"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/logger"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/storage"
)

const (
	backupSchedulerLockKey = "backup:scheduled-run"
	backupLockTTL          = 30 * time.Minute
	backupKeyPrefix        = "backups/"
)

// scheduledBackupState holds the pieces of BackupService only the scheduled
// path needs, kept separate from the manual Export/Restore/ImportRepo fields
// above so those keep working exactly as before for callers that never touch
// scheduling (e.g. every existing test in backup_service_test.go).
type scheduledBackupState struct {
	mu            sync.Mutex
	cronScheduler *cron.Cron
	entryID       cron.EntryID
	hasEntry      bool
}

// WithSettings attaches the scheduled-backup settings repo. Returns the same
// service for chaining, matching CleanupService.WithLocker's style.
func (s *BackupService) WithSettings(r repository.BackupSettingsRepo) *BackupService {
	s.Settings = r
	return s
}

// WithLocker sets the distributed locker used so a multi-replica deployment
// runs the scheduled backup once, not once per replica.
func (s *BackupService) WithLocker(l distlock.Locker) *BackupService {
	s.locker = l
	return s
}

// WithLogger sets the logger used by the scheduler and scheduled runs.
func (s *BackupService) WithLogger(l logger.Logger) *BackupService {
	s.log = l
	return s
}

// WithAudit sets the audit log a scheduled run's outcome is recorded to. Every
// other write to the audit trail goes through AuditMiddleware, which only
// sees real HTTP requests — a cron-triggered run never is one, so without
// this a failed scheduled backup would be visible only in structured logs
// and backup_settings.last_run_error, not in Security > Audit Log where an
// admin actually looks for "did last night's backup work."
func (s *BackupService) WithAudit(a repository.AuditRepo) *BackupService {
	s.audit = a
	return s
}

// RunScheduled exports a full backup and writes it to the configured
// destination blob store, then applies retention. Returns the written key
// (empty when it was a no-op) so callers can record it.
//
// No-op when scheduling is disabled or no destination store is configured —
// callers (the cron entry, or a manual "run now") get a nil error either way,
// since neither is a failure, just nothing to do yet.
func (s *BackupService) RunScheduled(ctx context.Context) (key string, err error) {
	if s.Settings == nil {
		return "", errors.New("backup: scheduled backup is not configured (no settings repo wired)")
	}
	settings, err := s.Settings.Get(ctx)
	if err != nil {
		return "", fmt.Errorf("backup: load settings: %w", err)
	}
	if !settings.Enabled || settings.BlobStoreID == "" {
		return "", nil
	}

	// Unlike storeFor's per-asset fallback in Export/Restore (defensible
	// there — every store still holds real product data, so "use the
	// default" is a reasonable best-effort), a resolution failure here must
	// be a hard error: the whole point of choosing a destination store is to
	// get bytes OUT of the default one, so silently falling back to it would
	// misdirect every scheduled backup to the wrong place while still
	// reporting success — exactly the class of bug this feature exists to
	// avoid, not reintroduce for itself.
	if s.Resolver == nil {
		return "", errors.New("backup: no store resolver configured, cannot resolve the destination blob store")
	}
	bs, err := s.BlobStores.GetByID(ctx, settings.BlobStoreID)
	if err != nil || bs == nil {
		return "", fmt.Errorf("backup: destination blob store %s not found", settings.BlobStoreID)
	}
	store, err := s.Resolver.Get(ctx, storage.BlobStoreDescriptor{ID: bs.ID, Type: bs.Type, Config: bs.Config})
	if err != nil || store == nil {
		return "", fmt.Errorf("backup: resolve destination store %s (%s): %w", bs.Name, bs.ID, err)
	}

	// Put needs a known size, and the archive's final size isn't known until
	// Export finishes writing it — spool to a temp file first, the same
	// approach backup_import.go already uses for the inbound side.
	tmp, err := os.CreateTemp("", "nexspence-scheduled-backup-*.tar.gz")
	if err != nil {
		return "", fmt.Errorf("backup: create spool file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if err := s.Export(ctx, tmp); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("backup: export: %w", err)
	}
	size, err := tmp.Seek(0, io.SeekCurrent)
	if err == nil {
		_, err = tmp.Seek(0, io.SeekStart)
	}
	if err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("backup: seek spool file: %w", err)
	}

	key = fmt.Sprintf("%snexspence-backup-%s.tar.gz", backupKeyPrefix, time.Now().UTC().Format("20060102-150405"))
	putErr := store.Put(ctx, key, tmp, size)
	_ = tmp.Close()
	if putErr != nil {
		return "", fmt.Errorf("backup: put %s: %w", key, putErr)
	}
	// Every other write path funnels through base.RegisterStoredBlob, which
	// keeps blob_stores.used_bytes (the DB counter quota checks actually
	// read, see base/store.go's quotaHeadroom) in sync with what's really in
	// the store. This path writes straight through store.Put and skips that
	// entirely, so a multi-GB backup would otherwise never count against its
	// destination's quota — do the same increment here (#490 review).
	if err := s.BlobStores.UpdateUsedBytes(ctx, bs.Name, size); err != nil {
		s.logWarn("backup: update used_bytes failed", "store", bs.Name, "err", err)
	}

	s.applyRetention(ctx, bs.Name, store, settings.RetentionCount)
	return key, nil
}

// applyRetention keeps the retentionCount most recently modified backups/
// entries in store, deleting older ones. No-op for retentionCount <= 0
// (unlimited) — an explicit opt-out, not the zero-value default (Get returns
// 7 when the settings row has never been written). storeName is the row name
// UpdateUsedBytes is keyed by — the same store RunScheduled just resolved bs
// from.
func (s *BackupService) applyRetention(ctx context.Context, storeName string, store storage.BlobStore, retentionCount int) {
	if retentionCount <= 0 {
		return
	}
	// A store shared with unrelated product data can hold far more than this
	// feature's own handful of backups — ask for just the "backups/" prefix
	// natively when the backend supports it (S3/Azure), instead of paging
	// through the whole store on every scheduled run just to find our own
	// entries (#490 review).
	var (
		entries []storage.BlobEntry
		err     error
	)
	if pl, ok := store.(storage.PrefixListableStore); ok {
		entries, err = pl.ListEntriesWithPrefix(ctx, backupKeyPrefix)
	} else {
		entries, err = store.ListEntries(ctx)
	}
	if err != nil {
		s.logWarn("backup retention: list entries failed", "err", err)
		return
	}
	var backups []storage.BlobEntry
	for _, e := range entries {
		if strings.HasPrefix(e.Key, backupKeyPrefix) {
			backups = append(backups, e)
		}
	}
	sort.Slice(backups, func(i, j int) bool { return backups[i].ModTime.After(backups[j].ModTime) })
	if len(backups) <= retentionCount {
		return
	}
	for _, e := range backups[retentionCount:] {
		if err := store.Delete(ctx, e.Key); err != nil {
			s.logWarn("backup retention: delete failed", "key", e.Key, "err", err)
			continue
		}
		if err := s.BlobStores.UpdateUsedBytes(ctx, storeName, -e.Size); err != nil {
			s.logWarn("backup retention: update used_bytes failed", "key", e.Key, "err", err)
		}
	}
}

// StartScheduler starts the cron entry for the configured schedule and blocks
// until ctx is canceled. Run as a goroutine (main.go).
func (s *BackupService) StartScheduler(ctx context.Context) {
	s.sched.mu.Lock()
	s.sched.cronScheduler = cron.New(cron.WithChain(cron.Recover(cron.DefaultLogger)))
	s.sched.mu.Unlock()

	if err := s.ReloadSchedule(ctx); err != nil {
		s.logError("backup: failed to load schedule", "err", err)
	}
	s.sched.cronScheduler.Start()
	<-ctx.Done()
	s.sched.cronScheduler.Stop()
}

// ReloadSchedule re-reads backup_settings and re-registers the cron entry, so
// a change made through PUT /api/v1/backup/settings takes effect without a
// restart — same UX as CleanupService.ReloadPolicy.
func (s *BackupService) ReloadSchedule(ctx context.Context) error {
	s.sched.mu.Lock()
	defer s.sched.mu.Unlock()
	if s.sched.cronScheduler == nil {
		return nil // scheduler not started yet — StartScheduler will call this itself
	}
	if s.sched.hasEntry {
		s.sched.cronScheduler.Remove(s.sched.entryID)
		s.sched.hasEntry = false
	}
	if s.Settings == nil {
		return nil
	}
	settings, err := s.Settings.Get(ctx)
	if err != nil {
		return err
	}
	if !settings.Enabled || settings.ScheduleCron == "" {
		return nil
	}
	id, err := s.sched.cronScheduler.AddFunc(settings.ScheduleCron, func() { s.runOnce(context.Background()) })
	if err != nil {
		return fmt.Errorf("backup: invalid schedule_cron %q: %w", settings.ScheduleCron, err)
	}
	s.sched.entryID, s.sched.hasEntry = id, true
	return nil
}

func (s *BackupService) runOnce(ctx context.Context) {
	if s.locker != nil {
		lock, err := s.locker.Acquire(ctx, backupSchedulerLockKey, backupLockTTL)
		if errors.Is(err, distlock.ErrLockHeld) {
			return // another replica is already running the scheduled backup
		}
		if err != nil {
			s.logWarn("backup: lock acquire failed, running unlocked", "err", err)
		} else {
			defer func() { _ = lock.Release(ctx) }()
		}
	}
	key, runErr := s.RunScheduled(ctx)
	if runErr == nil && key == "" {
		// Enabled but nothing to do yet (no destination chosen) — the cron
		// entry still fires on schedule since ReloadSchedule only gates on
		// Enabled/ScheduleCron, not BlobStoreID. Recording this as a "run"
		// would show a misleading "last run: just now" with nothing backed
		// up, so skip it rather than let the admin mistake it for a real one.
		return
	}
	errMsg := ""
	if runErr != nil {
		errMsg = runErr.Error()
		s.logError("scheduled backup failed", "err", runErr)
	} else {
		s.logInfo("scheduled backup complete", "key", key)
	}
	if s.Settings != nil {
		if err := s.Settings.RecordRun(ctx, time.Now(), key, errMsg); err != nil {
			s.logWarn("backup: record run failed", "err", err)
		}
	}
	s.recordAuditEvent(ctx, key, runErr)
}

// recordAuditEvent writes the outcome of a scheduled run to Security > Audit
// Log. Best-effort: an audit-write failure is logged but never turns a
// successful backup into a reported failure, or vice versa.
func (s *BackupService) recordAuditEvent(ctx context.Context, key string, runErr error) {
	if s.audit == nil {
		return
	}
	result := "success"
	ctxData := map[string]any{"key": key}
	if runErr != nil {
		result = "failure"
		ctxData["error"] = runErr.Error()
	}
	event := &domain.AuditEvent{
		EventTime:  time.Now(),
		Username:   "system",
		Domain:     "SYSTEM",
		Action:     "BACKUP",
		EntityType: "backup",
		EntityName: key,
		Context:    ctxData,
		Result:     result,
	}
	if err := s.audit.Write(ctx, event); err != nil {
		s.logWarn("backup: audit write failed", "err", err)
	}
}

// log* tolerate a nil s.log (scheduling wired up without a logger), matching
// how the rest of this codebase's optional loggers behave.
func (s *BackupService) logWarn(msg string, kv ...any) {
	if s.log != nil {
		s.log.Warnw(msg, kv...)
	}
}
func (s *BackupService) logError(msg string, kv ...any) {
	if s.log != nil {
		s.log.Errorw(msg, kv...)
	}
}
func (s *BackupService) logInfo(msg string, kv ...any) {
	if s.log != nil {
		s.log.Infow(msg, kv...)
	}
}
