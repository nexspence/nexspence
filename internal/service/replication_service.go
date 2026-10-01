package service

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"go.opentelemetry.io/otel/attribute"

	"github.com/nexspence-oss/nexspence/internal/distlock"
	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/logger"
	"github.com/nexspence-oss/nexspence/internal/netguard"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/storage"
	"github.com/nexspence-oss/nexspence/internal/tracing"
)

// ReplicationService pushes artifacts from local repos to remote Nexspence instances.
type ReplicationService struct {
	repo       repository.ReplicationRepo
	assets     repository.AssetRepo
	blobStore  storage.BlobStore
	blobs      repository.BlobStoreRepo
	resolver   StoreResolver
	primaryKey []byte // seals all new ciphertexts
	legacyKey  []byte // sha256(jwt_secret) fallback; nil when no dedicated key is set
	log        logger.Logger
	locker     distlock.Locker

	// running holds the rule IDs with an in-flight run (see RunRule).
	running sync.Map

	// newClient builds an HTTP client for a given timeout. Defaults to the
	// SSRF-guarded netguard.Client (target URLs are user-configured); tests
	// override it to reach loopback servers.
	newClient func(timeout time.Duration) *http.Client

	mu            sync.Mutex
	cronScheduler *cron.Cron
	entries       map[string]replicationCronEntry // rule ID → its cron entry
}

// replicationCronEntry is a rule's registered cron entry and the schedule it
// was registered with, so syncRules can tell when the stored one has changed.
// id is zero when the schedule does not parse: the rule is remembered without
// an entry so the sync does not retry it every pass.
type replicationCronEntry struct {
	id       cron.EntryID
	schedule string
}

// replicationRuleSyncSpec is how often each node re-reads the rules and brings
// its cron entries in line with them (see syncRules). A var, not a const, so
// tests can shorten it.
var replicationRuleSyncSpec = "@every 1m"

const replicationRuleLockPrefix = "nexspence:lock:replication:rule:"

// replicationLockTTL bounds a run holding a rule's lock. Nothing renews it, so
// a run stops pushing once it is reached (see runRule).
const replicationLockTTL = 30 * time.Minute

// NewReplicationService constructs a service that pushes artifacts to remote targets on a schedule.
func NewReplicationService(
	repo repository.ReplicationRepo,
	assets repository.AssetRepo,
	blobStore storage.BlobStore,
	jwtSecret string,
	encryptionKey []byte, // decoded auth.encryption_key; nil = legacy jwt-derived key
	log logger.Logger,
) *ReplicationService {
	s := &ReplicationService{
		repo:      repo,
		assets:    assets,
		blobStore: blobStore,
		log:       log,
		entries:   make(map[string]replicationCronEntry),
		newClient: netguard.Client,
	}
	legacy := deriveKey(jwtSecret)
	if len(encryptionKey) == 32 {
		s.primaryKey = encryptionKey
		s.legacyKey = legacy
	} else {
		s.primaryKey = legacy
	}
	return s
}

// WithResolver sets the blob-store repo and resolver so an asset is read from
// the physical store it actually lives on (S3, a second local store, ...)
// rather than the injected default. Without it the service falls back to
// blobStore, which is only correct while every asset sits in the default store.
func (s *ReplicationService) WithResolver(blobs repository.BlobStoreRepo, r StoreResolver) *ReplicationService {
	s.blobs = blobs
	s.resolver = r
	return s
}

// WithLocker sets the distributed locker that keeps nodes in an HA deployment
// from running the same rule at once. Without it the only exclusion is the
// per-process one in RunRule.
func (s *ReplicationService) WithLocker(l distlock.Locker) *ReplicationService {
	s.locker = l
	return s
}

// storeForAsset resolves the physical blob store an asset lives on, falling
// back to the default store when no resolver is configured, the asset carries
// no store id, or resolution fails.
func (s *ReplicationService) storeForAsset(ctx context.Context, a domain.Asset) storage.BlobStore {
	if s.resolver == nil || s.blobs == nil || a.BlobStoreID == "" {
		return s.blobStore
	}
	bs, err := s.blobs.GetByID(ctx, a.BlobStoreID)
	if err != nil || bs == nil {
		return s.blobStore
	}
	store, err := s.resolver.Get(ctx, storage.BlobStoreDescriptor{ID: bs.ID, Type: bs.Type, Config: bs.Config})
	if err != nil || store == nil {
		return s.blobStore
	}
	return store
}

// WithHTTPClientFactory overrides how HTTP clients are built (by timeout).
// Intended for tests that need to reach loopback servers the SSRF guard blocks.
func (s *ReplicationService) WithHTTPClientFactory(f func(timeout time.Duration) *http.Client) *ReplicationService {
	s.newClient = f
	return s
}

// EncryptPassword encrypts plain with AES-256-GCM under the primary key.
// Returns base64url(nonce + ciphertext). Returns "" for empty plain.
func (s *ReplicationService) EncryptPassword(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	return sealWithKey(s.primaryKey, plain)
}

// DecryptPassword decrypts enc, falling back to the legacy jwt-derived key
// for rows sealed before auth.encryption_key was adopted.
func (s *ReplicationService) DecryptPassword(enc string) (string, error) {
	plain, _, err := s.decryptDetect(enc)
	return plain, err
}

// decryptDetect reports whether the legacy key was needed (true = the stored
// row should be re-encrypted under the primary key).
func (s *ReplicationService) decryptDetect(enc string) (string, bool, error) {
	if enc == "" {
		return "", false, nil
	}
	plain, err := openWithKey(s.primaryKey, enc)
	if err == nil {
		return plain, false, nil
	}
	if s.legacyKey != nil {
		if lp, lerr := openWithKey(s.legacyKey, enc); lerr == nil {
			return lp, true, nil
		}
	}
	return "", false, err
}

func sealWithKey(key []byte, plain string) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plain), nil)
	return base64.URLEncoding.EncodeToString(sealed), nil
}

func openWithKey(key []byte, enc string) (string, error) {
	data, err := base64.URLEncoding.DecodeString(enc)
	if err != nil {
		return "", fmt.Errorf("replication: base64 decode: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	ns := gcm.NonceSize()
	if len(data) < ns {
		return "", fmt.Errorf("replication: ciphertext too short")
	}
	plain, err := gcm.Open(nil, data[:ns], data[ns:], nil)
	if err != nil {
		return "", fmt.Errorf("replication: decrypt: %w", err)
	}
	return string(plain), nil
}

func deriveKey(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

// ReEncryptCredentials migrates credentials sealed with the legacy jwt-derived
// key to the dedicated encryption key. Idempotent: rows already sealed with the
// primary key are skipped, so concurrent HA replicas and restarts are safe.
// Returns the number of migrated rules.
func (s *ReplicationService) ReEncryptCredentials(ctx context.Context) int {
	rules, err := s.repo.ListRules(ctx)
	if err != nil {
		s.log.Error("replication: re-encryption sweep: list rules", "err", err)
		return 0
	}
	migrated := 0
	for i := range rules {
		rule := rules[i]
		if rule.TargetPasswordEnc == "" {
			continue
		}
		plain, usedLegacy, err := s.decryptDetect(rule.TargetPasswordEnc)
		if err != nil {
			s.log.Warn("replication: credentials cannot be decrypted with any key — re-enter the password", "rule", rule.Name, "err", err)
			continue
		}
		if !usedLegacy {
			continue
		}
		enc, err := sealWithKey(s.primaryKey, plain)
		if err != nil {
			s.log.Error("replication: re-encrypt credentials", "rule", rule.Name, "err", err)
			continue
		}
		rule.TargetPasswordEnc = enc
		if err := s.repo.UpdateRule(ctx, &rule); err != nil {
			s.log.Error("replication: persist re-encrypted credentials", "rule", rule.Name, "err", err)
			continue
		}
		migrated++
	}
	if migrated > 0 {
		s.log.Info("replication: re-encrypted credentials under dedicated encryption key", "migrated", migrated, "rules", len(rules))
	}
	return migrated
}

// StartCronScheduler loads all enabled rules and registers cron jobs. Run as a goroutine.
func (s *ReplicationService) StartCronScheduler(ctx context.Context) {
	s.mu.Lock()
	// cron.Recover: a job panic runs on cron's own internal scheduler
	// goroutine and would otherwise crash the whole process — not covered by
	// the safego.Go wrapping the outer StartCronScheduler call.
	s.cronScheduler = cron.New(cron.WithChain(cron.Recover(cron.DefaultLogger)))
	s.mu.Unlock()

	rules, err := s.repo.ListRules(ctx)
	if err != nil {
		s.log.Errorw("replication: failed to load rules for scheduler", "err", err)
	} else {
		if s.legacyKey == nil && len(rules) > 0 {
			s.log.Warn("replication: stored credentials are encrypted with a key derived from auth.jwt_secret; set auth.encryption_key to decouple them (rotating jwt_secret would otherwise invalidate them)")
		}
		if s.legacyKey != nil {
			s.ReEncryptCredentials(ctx)
		}
	}

	s.syncRules(ctx)
	// ReloadRule only runs on the node that served the create, update or
	// delete. Every other node picks the change up here, so the per-rule lock,
	// not which node saved the rule, decides who runs it (#574).
	if _, err := s.cronScheduler.AddFunc(replicationRuleSyncSpec, func() { s.syncRules(context.Background()) }); err != nil {
		s.log.Errorw("replication: failed to start rule sync", "err", err)
	}

	s.cronScheduler.Start()
	<-ctx.Done()
	s.cronScheduler.Stop()
}

// syncRules re-reads every rule and brings the cron entries in line: adds
// entries for enabled rules this node does not know yet, re-registers those
// whose schedule changed, and removes those disabled or deleted. A failed read
// changes nothing, so a database hiccup does not drop schedules.
func (s *ReplicationService) syncRules(ctx context.Context) {
	rules, err := s.repo.ListRules(ctx)
	if err != nil {
		s.log.Errorw("replication: failed to load rules for scheduler", "err", err)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cronScheduler == nil {
		return
	}
	want := make(map[string]string, len(rules))
	for _, r := range rules {
		if r.Enabled {
			want[r.ID] = r.CronExpr
		}
	}
	for id, e := range s.entries {
		if schedule, ok := want[id]; !ok || schedule != e.schedule {
			s.removeEntryLocked(id)
		}
	}
	for _, r := range rules {
		if _, ok := s.entries[r.ID]; !ok && r.Enabled {
			s.addEntryLocked(r)
		}
	}
}

// ReloadRule updates the cron entry for a single rule (call after
// Create/Update/Delete). Other nodes pick the change up on their next
// syncRules.
func (s *ReplicationService) ReloadRule(ctx context.Context, ruleID string) {
	rule, err := s.repo.GetRule(ctx, ruleID)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		// A transient lookup failure must not unschedule the rule: keeping a
		// possibly-stale cron entry beats a rule that silently never runs
		// again until the next restart (#254). ErrNotFound is different — the
		// rule really is gone, and its entry goes with it below.
		s.log.Warnw("replication: reload lookup failed, keeping existing schedule", "rule", ruleID, "err", err)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cronScheduler == nil {
		return
	}
	s.removeEntryLocked(ruleID)
	if rule == nil || !rule.Enabled {
		return
	}
	s.addEntryLocked(*rule)
}

// removeEntryLocked drops the rule's cron entry, if any. Caller must hold s.mu.
func (s *ReplicationService) removeEntryLocked(ruleID string) {
	if e, ok := s.entries[ruleID]; ok {
		if e.id != 0 {
			s.cronScheduler.Remove(e.id)
		}
		delete(s.entries, ruleID)
	}
}

// addEntryLocked registers a cron job for rule. Caller must hold s.mu.
func (s *ReplicationService) addEntryLocked(rule domain.ReplicationRule) {
	ruleID, schedule := rule.ID, rule.CronExpr
	if schedule == "" {
		// No schedule: the rule only runs when triggered manually.
		s.entries[ruleID] = replicationCronEntry{}
		return
	}
	job := func() { s.runScheduled(context.Background(), ruleID, schedule) }
	id, err := s.cronScheduler.AddFunc(schedule, job)
	if err != nil {
		s.log.Warnw("replication: invalid cron_expr, skipping rule", "rule", rule.Name, "expr", schedule, "err", err)
		id = 0
	}
	s.entries[ruleID] = replicationCronEntry{id: id, schedule: schedule}
}

// runScheduled is a rule's cron job. It reads the rule when it fires: until
// this node's next syncRules its entry may predate a change saved on another
// node, and a rule since disabled or deleted must not run, nor one whose
// schedule was changed away from the one this entry was registered with.
func (s *ReplicationService) runScheduled(ctx context.Context, ruleID, registered string) {
	rule, err := s.repo.GetRule(ctx, ruleID)
	if err != nil || rule == nil {
		// Deleted on another node or unreadable: nothing safe to run. The
		// next syncRules drops the entry of a deleted rule.
		if err != nil && !errors.Is(err, repository.ErrNotFound) {
			s.log.Warnw("replication cron: rule not loaded, run skipped", "rule", ruleID, "err", err)
		}
		return
	}
	if !rule.Enabled || rule.CronExpr != registered {
		// Disabled or rescheduled on another node: the entry goes, or the one
		// for the new schedule arrives, with the next syncRules.
		return
	}
	// The slot comes from the moment the tick fired, before waiting on the
	// lock or the database, so neither can move it.
	slot := replicationSlotStart(registered, time.Now())
	deadline, release, err := s.acquireRun(ctx, ruleID)
	if errors.Is(err, ErrReplicationRuleRunning) {
		s.log.Infow("replication skipped: the rule is already running", "rule", rule.Name, "reason", err)
		return
	}
	if err != nil {
		s.log.Errorw("replication cron error", "rule", rule.Name, "err", err)
		return
	}
	defer release()
	// The lock only covers a run in progress. A node whose tick fires after
	// another node already finished this slot's run would otherwise run it
	// again — two history rows and twice the traffic per slot (#574). Checked
	// under the lock, so the other node's history row is already written.
	if s.ranSince(ctx, ruleID, slot) {
		s.log.Infow("replication skipped: this slot already ran on another node", "rule", rule.Name)
		return
	}
	if err := s.execute(ctx, ruleID, deadline); err != nil {
		s.log.Errorw("replication cron error", "rule", rule.Name, "err", err)
	}
}

// replicationSlotSkew is how far apart two nodes' clocks may be and still be
// recognized as firing for the same cron slot (see replicationSlotStart).
const replicationSlotSkew = 10 * time.Second

// replicationSlotStart is the earliest start time of a run that belongs to
// the same cron slot as a tick firing at now. Standard specs fire on whole
// minutes, so the slot is now truncated to the minute, widened by
// replicationSlotSkew for a node whose clock runs ahead. @every intervals are
// not aligned across nodes (each counts from its own registration) and may be
// under a minute, so for them it is half an interval back.
func replicationSlotStart(schedule string, now time.Time) time.Time {
	if sched, err := cron.ParseStandard(schedule); err == nil {
		if every, ok := sched.(cron.ConstantDelaySchedule); ok {
			return now.Add(-every.Delay / 2)
		}
	}
	return now.Truncate(time.Minute).Add(-replicationSlotSkew)
}

// ranSince reports whether the rule's latest recorded run started at or after
// slot. A history read failure is not a reason to skip a run.
func (s *ReplicationService) ranSince(ctx context.Context, ruleID string, slot time.Time) bool {
	hist, err := s.repo.ListHistory(ctx, ruleID, 1)
	if err != nil || len(hist) == 0 {
		return false
	}
	return !hist[0].StartedAt.Before(slot)
}

// Running reports whether a run of the rule is in flight in this process.
func (s *ReplicationService) Running(ruleID string) bool {
	_, busy := s.running.Load(ruleID)
	return busy
}

// ErrReplicationRuleRunning is returned when a rule cannot start because a run
// of it is already in flight, in this process or on another HA node.
var ErrReplicationRuleRunning = errors.New("replication rule is already running")

// acquireRun takes the rule's in-process guard and, with a locker, its
// distributed lock. release undoes both and must be called once. deadline is
// when the lock expires (zero: no lock).
//
// One run per rule per process: now that manual runs are detached from the
// request context they no longer die by accident, so repeated POSTs to
// .../run would otherwise pile up concurrent runs of the same rule pushing
// the same assets. Across nodes the rule's distributed lock does the same
// (#574).
func (s *ReplicationService) acquireRun(ctx context.Context, ruleID string) (deadline time.Time, release func(), err error) {
	if _, busy := s.running.LoadOrStore(ruleID, struct{}{}); busy {
		return time.Time{}, nil, fmt.Errorf("%w: %s", ErrReplicationRuleRunning, ruleID)
	}
	if s.locker == nil {
		return time.Time{}, func() { s.running.Delete(ruleID) }, nil
	}
	lock, err := s.locker.Acquire(ctx, replicationRuleLockPrefix+ruleID, replicationLockTTL)
	if err != nil {
		s.running.Delete(ruleID)
		if errors.Is(err, distlock.ErrLockHeld) {
			return time.Time{}, nil, fmt.Errorf("%w on another node: %s: %w", ErrReplicationRuleRunning, ruleID, err)
		}
		// Without the lock there is no exclusion: stop rather than push
		// alongside another node.
		return time.Time{}, nil, fmt.Errorf("replication: acquire lock for rule %s: %w", ruleID, err)
	}
	releaseCtx := context.WithoutCancel(ctx)
	return time.Now().Add(replicationLockTTL), func() {
		_ = lock.Release(releaseCtx)
		s.running.Delete(ruleID)
	}, nil
}

// RunRule executes a single replication rule immediately and waits for it.
// A rule already running here or on another node is refused with an error
// wrapping ErrReplicationRuleRunning, and records nothing.
func (s *ReplicationService) RunRule(ctx context.Context, ruleID string) error {
	deadline, release, err := s.acquireRun(ctx, ruleID)
	if err != nil {
		return err
	}
	defer release()
	return s.execute(ctx, ruleID, deadline)
}

// StartRule claims the rule for a run (in-process guard and distributed lock)
// and returns the run to execute, typically in a goroutine. Claiming up front
// lets the manual-run endpoint refuse a rule already running here or on
// another node with 409, instead of answering 202 for a run that is then
// dropped. The returned func must be called exactly once.
func (s *ReplicationService) StartRule(ctx context.Context, ruleID string) (func(context.Context) error, error) {
	deadline, release, err := s.acquireRun(ctx, ruleID)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) error {
		defer release()
		return s.execute(ctx, ruleID, deadline)
	}, nil
}

// execute runs a rule the caller has claimed with acquireRun and records its
// history.
func (s *ReplicationService) execute(ctx context.Context, ruleID string, deadline time.Time) error {
	// Root span: every caller (the manual-trigger handler, the tasks endpoint
	// and cron) runs this as a background job. It is also what makes
	// cross-process propagation work at all — with no span in context,
	// injecting traceparent into the outgoing requests is silently a no-op
	// (#302).
	ctx, span := tracing.StartRoot(ctx, "replication.run_rule",
		attribute.String("replication.rule_id", ruleID))
	defer span.End()

	rule, err := s.repo.GetRule(ctx, ruleID)
	if err != nil {
		return err
	}
	if rule == nil {
		return fmt.Errorf("replication rule %q not found", ruleID)
	}

	_ = s.repo.UpdateRuleStatus(ctx, ruleID, "running", time.Now())

	hist := &domain.ReplicationHistory{
		RuleID:    ruleID,
		StartedAt: time.Now(),
	}

	runErr := s.runRule(ctx, rule, hist, deadline)

	now := time.Now()
	hist.FinishedAt = &now
	hist.DurationMs = now.Sub(hist.StartedAt).Milliseconds()

	status := "ok"
	if runErr != nil || hist.FailedCount > 0 {
		status = "error"
		if runErr != nil {
			hist.Error = runErr.Error()
		}
	}
	// Record the outcome even when the run was cut by its context: on that
	// context both writes fail, leaving the rule "running" with no history
	// row until another run of it finishes (#573).
	recCtx := context.WithoutCancel(ctx)
	_ = s.repo.UpdateRuleStatus(recCtx, ruleID, status, now)
	_ = s.repo.AddHistory(recCtx, hist)

	return runErr
}

// runRule performs the actual diff + push for a rule. deadline is the moment
// the run's distributed lock expires (zero: no lock). Nothing renews the lock,
// so past that point another node can start the same rule; the run stops
// pushing there instead of continuing unprotected (#371), and the next run
// diffs against the target again and pushes the rest.
func (s *ReplicationService) runRule(ctx context.Context, rule *domain.ReplicationRule, hist *domain.ReplicationHistory, deadline time.Time) error {
	password, err := s.DecryptPassword(rule.TargetPasswordEnc)
	if err != nil {
		return fmt.Errorf("decrypt credentials: %w", err)
	}

	targetPaths, err := s.listTargetPaths(ctx, rule, password)
	if err != nil {
		return fmt.Errorf("list target assets: %w", err)
	}

	localAssets, err := s.assets.ListByRepoAndPath(ctx, rule.SourceRepo, "")
	if err != nil {
		return fmt.Errorf("list local assets: %w", err)
	}

	client := s.newClient(5 * time.Minute)
	for _, asset := range localAssets {
		if pastDeadline(deadline) {
			logger.WithTraceContext(ctx, s.log).Warnw("replication: lock deadline reached, remaining assets left for the next run",
				"rule", rule.Name, "pushed", hist.PushedCount)
			break
		}
		if _, exists := targetPaths[asset.Path]; exists {
			hist.SkippedCount++
			continue
		}

		pushed, transferred, pushErr := s.pushAsset(ctx, client, rule, password, asset)
		if pushErr != nil {
			hist.FailedCount++
			if hist.Error == "" {
				hist.Error = pushErr.Error()
			}
			logger.WithTraceContext(ctx, s.log).Warn("replication: push failed", "rule", rule.Name, "path", asset.Path, "err", pushErr)
			continue
		}
		if pushed {
			hist.PushedCount++
			hist.TransferredBytes += transferred
		}
	}
	return nil
}

// listTargetPaths queries the target instance for all asset paths in targetRepo.
func (s *ReplicationService) listTargetPaths(ctx context.Context, rule *domain.ReplicationRule, password string) (map[string]struct{}, error) {
	paths := make(map[string]struct{})
	client := s.newClient(30 * time.Second)
	token := ""

	for {
		url := strings.TrimRight(rule.TargetURL, "/") +
			"/service/rest/v1/assets?repository=" + rule.TargetRepo
		if token != "" {
			url += "&continuationToken=" + token
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		// Propagate the trace across the process boundary (W3C traceparent):
		// the receiving nexspence continues this trace (#302).
		tracing.Inject(ctx, req.Header)
		if rule.TargetUsername != "" {
			req.SetBasicAuth(rule.TargetUsername, password)
		}

		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("target returned %d: %s", resp.StatusCode, string(body))
		}

		var page struct {
			Items []struct {
				Path string `json:"path"`
			} `json:"items"`
			ContinuationToken *string `json:"continuationToken"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("parse target response: %w", err)
		}

		for _, item := range page.Items {
			paths[item.Path] = struct{}{}
		}

		if page.ContinuationToken == nil || *page.ContinuationToken == "" {
			break
		}
		token = *page.ContinuationToken
	}
	return paths, nil
}

// pushAsset streams one blob to the target. Returns (pushed, bytes, error).
func (s *ReplicationService) pushAsset(ctx context.Context, client *http.Client, rule *domain.ReplicationRule, password string, asset domain.Asset) (bool, int64, error) {
	rc, size, err := s.storeForAsset(ctx, asset).Get(ctx, asset.BlobKey)
	if err != nil {
		return false, 0, fmt.Errorf("fetch blob %s: %w", asset.BlobKey, err)
	}
	defer func() { _ = rc.Close() }()

	targetPath := strings.TrimRight(rule.TargetURL, "/") +
		"/repository/" + rule.TargetRepo + "/" + strings.TrimPrefix(asset.Path, "/")

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, targetPath, rc)
	if err != nil {
		return false, 0, err
	}
	// Propagate the trace across the process boundary (W3C traceparent):
	// the receiving nexspence continues this trace (#302).
	tracing.Inject(ctx, req.Header)
	if size > 0 {
		req.ContentLength = size
	}
	if rule.TargetUsername != "" {
		req.SetBasicAuth(rule.TargetUsername, password)
	}
	if asset.ContentType != "" {
		req.Header.Set("Content-Type", asset.ContentType)
	}

	resp, err := client.Do(req)
	if err != nil {
		return false, 0, err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	if resp.StatusCode >= 400 {
		return false, 0, fmt.Errorf("target PUT %s returned %d", asset.Path, resp.StatusCode)
	}
	return true, size, nil
}

// TestConnection verifies connectivity and credentials to a target rule.
func (s *ReplicationService) TestConnection(ctx context.Context, ruleID string) error {
	rule, err := s.repo.GetRule(ctx, ruleID)
	if err != nil {
		return err
	}
	if rule == nil {
		return fmt.Errorf("rule not found")
	}
	password, err := s.DecryptPassword(rule.TargetPasswordEnc)
	if err != nil {
		return err
	}

	url := strings.TrimRight(rule.TargetURL, "/") + "/service/rest/v1/status"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	// Propagate the trace across the process boundary (W3C traceparent):
	// the receiving nexspence continues this trace (#302).
	tracing.Inject(ctx, req.Header)
	if rule.TargetUsername != "" {
		req.SetBasicAuth(rule.TargetUsername, password)
	}

	client := s.newClient(10 * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("target returned %d", resp.StatusCode)
	}
	return nil
}

// ListRules returns all replication rules (passwords masked).
func (s *ReplicationService) ListRules(ctx context.Context) ([]domain.ReplicationRule, error) {
	rules, err := s.repo.ListRules(ctx)
	if err != nil {
		return nil, err
	}
	for i := range rules {
		rules[i].TargetPasswordEnc = ""
	}
	return rules, nil
}

// GetRule returns a single rule (password masked).
func (s *ReplicationService) GetRule(ctx context.Context, id string) (*domain.ReplicationRule, error) {
	rule, err := s.repo.GetRule(ctx, id)
	if err != nil || rule == nil {
		return rule, err
	}
	rule.TargetPasswordEnc = ""
	return rule, nil
}

// validateCronExpr rejects a schedule the cron scheduler cannot parse. An empty
// expression is allowed and means "no schedule" — the rule only runs when it is
// triggered manually. Anything else, left unvalidated, would be dropped by
// addEntryLocked with nothing but a log line, leaving the rule permanently
// unscheduled while the API reported success.
func validateCronExpr(expr string) error {
	if expr == "" {
		return nil
	}
	if _, err := cron.ParseStandard(expr); err != nil {
		return fmt.Errorf("invalid cron_expr %q: %w", expr, err)
	}
	return nil
}

// CreateRule encrypts the password and persists the rule.
func (s *ReplicationService) CreateRule(ctx context.Context, rule *domain.ReplicationRule, plainPassword string) error {
	if err := validateCronExpr(rule.CronExpr); err != nil {
		return err
	}
	enc, err := s.EncryptPassword(plainPassword)
	if err != nil {
		return err
	}
	rule.TargetPasswordEnc = enc
	return s.repo.CreateRule(ctx, rule)
}

// UpdateRule encrypts the password if provided (non-empty), otherwise keeps existing.
func (s *ReplicationService) UpdateRule(ctx context.Context, rule *domain.ReplicationRule, plainPassword string) error {
	if err := validateCronExpr(rule.CronExpr); err != nil {
		return err
	}
	if plainPassword != "" {
		enc, err := s.EncryptPassword(plainPassword)
		if err != nil {
			return err
		}
		rule.TargetPasswordEnc = enc
	} else {
		existing, err := s.repo.GetRule(ctx, rule.ID)
		if err != nil && !errors.Is(err, repository.ErrNotFound) {
			return err
		}
		if existing != nil {
			rule.TargetPasswordEnc = existing.TargetPasswordEnc
		}
	}
	return s.repo.UpdateRule(ctx, rule)
}

// DeleteRule removes the rule and its cron entry.
func (s *ReplicationService) DeleteRule(ctx context.Context, id string) error {
	if err := s.repo.DeleteRule(ctx, id); err != nil {
		return err
	}
	s.ReloadRule(ctx, id)
	return nil
}

// ListHistory returns the last N history entries for a rule.
func (s *ReplicationService) ListHistory(ctx context.Context, ruleID string, limit int) ([]domain.ReplicationHistory, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	return s.repo.ListHistory(ctx, ruleID, limit)
}
