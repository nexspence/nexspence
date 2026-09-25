package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/logger"
	"github.com/nexspence-oss/nexspence/internal/repository"
)

// Automatic promotion on publish (#542).
//
// A rule with auto_promote set starts by itself when a client publishes into
// its from_repo. The upload path only records the publish (NotifyPublished: one
// INSERT ... SELECT into promotion_auto_queue); everything else happens in a
// background worker, off the request:
//
//  1. Settle. A component is evaluated once no asset has arrived for the
//     settle window, so a Maven jar+pom+sources deploy or a multi-wheel PyPI
//     upload is promoted once, whole — every publish restarts the window.
//  2. Gates. The rule's own gates apply exactly as on a manual Promote: path
//     filter, the ambiguity check against sibling rules, require_scan_pass
//     with the rule's severities, the target's write policy, and for an image
//     the #541 completeness check. A scan gate waits for a scan of this
//     publish (see awaitScan); every other refusal is final for this publish
//     and is recorded as a failed automatic request with the reason, plus an
//     audit event — never retried forever.
//  3. Request. With require_manual_approval a pending automatic request is
//     filed — at most one per (rule, component), however often the component
//     is published while it waits — and Approve copies as usual. Without it,
//     the request is filed and copied at once under its row lock, like an
//     auto-approved Promote.
//
// A component auto-promoted before is promoted again when it grows: the copy
// then writes only what the target lacks (planCopy's onlyMissing), so the
// sources jar deployed after the release reaches the target too, and an
// allow_once target refuses only a path whose content really changed.
//
// Promotion's own copies are not publishes (they do not go through
// base.RegisterStoredBlob), so an auto rule on the target repository is not
// started by them: there are no chains A→B→C, and hence no cycles.
//
// HA: rows are claimed with FOR UPDATE SKIP LOCKED and a lease, so replicas
// share the queue without a distributed lock and a crashed node's row is
// picked up once its lease runs out. The copy itself runs under the request's
// row lock (WithPendingRequestLock), so even a row processed twice — a lease
// outlived by a very large copy — cannot copy twice.

// AutoPromotionOptions tunes the auto-promotion worker. Zero values take the
// defaults below.
type AutoPromotionOptions struct {
	// SettleWindow is how long a component must go without a new asset before
	// it is evaluated.
	SettleWindow time.Duration
	// ScanWait is how long a rule that requires a scan waits for a scan of the
	// publish before the promotion is recorded as blocked.
	ScanWait time.Duration
	// PollInterval is how often the worker looks for due rows.
	PollInterval time.Duration
	// Lease is how long a claimed row stays with the replica that claimed it.
	Lease time.Duration
	// BatchSize caps the rows claimed per poll.
	BatchSize int
	// MaxAttempts caps retries of an evaluation that keeps failing on
	// something transient (a database error) before it is recorded as blocked.
	MaxAttempts int
	// Now is the clock; nil means time.Now. A test seam.
	Now func() time.Time
}

// Auto-promotion defaults.
const (
	DefaultAutoPromotionSettleWindow = 30 * time.Second
	DefaultAutoPromotionScanWait     = time.Hour
	DefaultAutoPromotionPollInterval = 5 * time.Second
	defaultAutoPromotionLease        = 30 * time.Minute
	defaultAutoPromotionBatch        = 20
	defaultAutoPromotionMaxAttempts  = 20

	// autoRetryBase and autoRetryMax bound the backoff between re-checks of a
	// row waiting for a scan or retrying a transient failure.
	autoRetryBase = 15 * time.Second
	autoRetryMax  = 5 * time.Minute

	// autoNotifyTimeout bounds the enqueue on the upload path.
	autoNotifyTimeout = 5 * time.Second
)

// Audit actions for auto-promotion; the domain is "PROMOTION".
const (
	AuditDomainPromotion      = "PROMOTION"
	AuditAutoPromoteStarted   = "AUTO_PROMOTE_STARTED"
	AuditAutoPromotePending   = "AUTO_PROMOTE_PENDING"
	AuditAutoPromoteCopied    = "AUTO_PROMOTE_COPIED"
	AuditAutoPromoteBlocked   = "AUTO_PROMOTE_BLOCKED"
	autoPromotionAuditActor   = "system"
	autoPromotionAuditSubject = "COMPONENT"
)

func (o AutoPromotionOptions) withDefaults() AutoPromotionOptions {
	if o.SettleWindow < 0 {
		o.SettleWindow = 0
	}
	if o.ScanWait <= 0 {
		o.ScanWait = DefaultAutoPromotionScanWait
	}
	if o.PollInterval <= 0 {
		o.PollInterval = DefaultAutoPromotionPollInterval
	}
	if o.Lease <= 0 {
		o.Lease = defaultAutoPromotionLease
	}
	if o.BatchSize <= 0 {
		o.BatchSize = defaultAutoPromotionBatch
	}
	if o.MaxAttempts <= 0 {
		o.MaxAttempts = defaultAutoPromotionMaxAttempts
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return o
}

// autoPromotion is the worker state a PromotionService carries once
// WithAutoPromotion wires it.
type autoPromotion struct {
	queue repository.AutoPromotionQueueRepo
	audit repository.AuditRepo
	log   logger.Logger
	opts  AutoPromotionOptions
}

// WithAutoPromotion enables auto-promotion on publish over queue and returns s.
// audit and log may be nil. A settle window of zero is honored (evaluate on
// the next poll); use DefaultAutoPromotionSettleWindow for the default.
func (s *PromotionService) WithAutoPromotion(queue repository.AutoPromotionQueueRepo, audit repository.AuditRepo,
	log logger.Logger, opts AutoPromotionOptions) *PromotionService {
	if log == nil {
		log = zap.NewNop().Sugar()
	}
	s.auto = &autoPromotion{queue: queue, audit: audit, log: log, opts: opts.withDefaults()}
	return s
}

// NotifyPublished records that a client published into componentID in
// repository repoName (formats.PublishNotifier). It is on the upload path: one
// statement, bounded by a timeout, detached from the request's cancellation (a
// client hanging up after its upload succeeded must not lose the record), and
// never an error to the caller.
func (s *PromotionService) NotifyPublished(ctx context.Context, repoName, componentID string) {
	if s.auto == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), autoNotifyTimeout)
	defer cancel()
	now := s.auto.opts.Now()
	if _, err := s.auto.queue.EnqueuePublish(ctx, repoName, componentID, now, now.Add(s.auto.opts.SettleWindow)); err != nil {
		s.auto.log.Warnw("auto-promotion: publish not recorded", "repository", repoName, "component", componentID, "err", err)
	}
}

// NotifyScanned wakes rows waiting for a scan of componentID; the scan service
// calls it when it stores a result. Without it the worker still finds the
// scan on its next backoff-paced re-check — this only makes it prompt.
func (s *PromotionService) NotifyScanned(ctx context.Context, componentID string) {
	if s.auto == nil {
		return
	}
	if err := s.auto.queue.WakeForScan(ctx, componentID, s.auto.opts.Now()); err != nil {
		s.auto.log.Warnw("auto-promotion: scan wake-up failed", "component", componentID, "err", err)
	}
}

// RunAutoPromotion drains the auto-promotion queue every PollInterval until
// ctx is done. Run it as a goroutine on every replica.
func (s *PromotionService) RunAutoPromotion(ctx context.Context) {
	if s.auto == nil {
		return
	}
	t := time.NewTicker(s.auto.opts.PollInterval)
	defer t.Stop()
	for {
		for {
			n, err := s.ProcessAutoPromotions(ctx)
			if err != nil {
				s.auto.log.Warnw("auto-promotion: claim failed", "err", err)
				break
			}
			if n < s.auto.opts.BatchSize || ctx.Err() != nil {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// ProcessAutoPromotions claims the due rows and evaluates each one; it
// returns how many it claimed. One pass of RunAutoPromotion.
func (s *PromotionService) ProcessAutoPromotions(ctx context.Context) (int, error) {
	if s.auto == nil {
		return 0, nil
	}
	o := s.auto.opts
	entries, err := s.auto.queue.Claim(ctx, o.Now(), o.Lease, o.BatchSize)
	if err != nil {
		return 0, err
	}
	for i := 0; i < len(entries) && ctx.Err() == nil; i++ {
		s.processAutoEntry(ctx, entries[i])
	}
	return len(entries), nil
}

// autoRun is one evaluation of one queue row.
type autoRun struct {
	s    *PromotionService
	e    domain.AutoPromotionEntry
	rule *domain.PromotionRule
	comp *domain.Component
}

func (s *PromotionService) processAutoEntry(ctx context.Context, e domain.AutoPromotionEntry) {
	r := &autoRun{s: s, e: e}
	o := s.auto.opts

	rule, err := s.promotionRepo.GetRule(ctx, e.RuleID)
	if lookupMissing(rule, err) {
		r.finish(ctx) // the rule is gone
		return
	}
	if err != nil {
		r.retryTransient(ctx, fmt.Errorf("load rule: %w", err))
		return
	}
	r.rule = rule
	if !rule.AutoPromote {
		r.finish(ctx) // switched off since the publish
		return
	}
	comp, err := s.componentRepo.Get(ctx, e.ComponentID)
	if lookupMissing(comp, err) {
		r.finish(ctx) // deleted since the publish
		return
	}
	if err != nil {
		r.retryTransient(ctx, fmt.Errorf("load component: %w", err))
		return
	}
	r.comp = comp
	if comp.Repository != rule.FromRepo {
		r.finish(ctx) // the rule was re-pointed since the publish
		return
	}

	// The window may have been lengthened since the row was queued.
	if settled := e.LastPublishedAt.Add(o.SettleWindow); o.Now().Before(settled) {
		r.retry(ctx, settled, false, "settling")
		return
	}

	matched, ferr := s.evalPathFilter(*rule, comp)
	if ferr != nil {
		r.blocked(ctx, fmt.Errorf("rule %q: %w", rule.Name, ferr))
		return
	}
	if !matched {
		r.finish(ctx) // not this rule's component: nothing to record
		return
	}
	if err := s.noSiblingRuleApplies(ctx, rule, comp); err != nil {
		r.blocked(ctx, err)
		return
	}

	if e.Attempts == 0 {
		r.auditEvent(ctx, AuditAutoPromoteStarted, "success", nil)
	}

	if rule.RequireScanPass {
		if waiting := r.awaitScan(ctx); waiting {
			return
		}
		if err := s.scanGate(ctx, rule, comp.ID); err != nil {
			r.blocked(ctx, err)
			return
		}
	}

	toRepo, err := s.repoRepo.Get(ctx, rule.ToRepo)
	if lookupMissing(toRepo, err) {
		r.blocked(ctx, fmt.Errorf("target repository not found: %s", rule.ToRepo))
		return
	}
	if err != nil {
		r.retryTransient(ctx, fmt.Errorf("load target repository: %w", err))
		return
	}
	plan, err := s.planCopy(ctx, comp, toRepo, true)
	if err != nil {
		// An incomplete image, a write-policy refusal: final for this publish.
		r.blocked(ctx, err)
		return
	}
	if plan.empty() {
		// The target already holds all of it — a re-upload of identical bytes.
		r.finish(ctx)
		return
	}

	req := &domain.PromotionRequest{
		RuleID:             rule.ID,
		ComponentID:        comp.ID,
		Status:             domain.PromotionPending,
		IncludedComponents: plan.dependents,
	}
	created, err := s.promotionRepo.CreateAutoRequest(ctx, req)
	if err != nil {
		r.retryTransient(ctx, fmt.Errorf("file request: %w", err))
		return
	}
	if rule.RequireManualApproval {
		if created {
			r.auditEvent(ctx, AuditAutoPromotePending, "success", map[string]any{"request_id": req.ID})
		}
		r.finish(ctx)
		return
	}
	r.copyNow(ctx, req)
	r.finish(ctx)
}

// awaitScan reports whether the row must keep waiting for a scan, having
// rescheduled it (or recorded it blocked) if so.
//
// Only a scan of this publish counts: one that started no earlier than the
// component's last asset. Without that, a re-pushed tag or a redeployed
// SNAPSHOT would pass on the previous content's clean scan while its new
// content is still unscanned. The scan is not triggered here — every upload
// already queues one (base.queueForScanning); this waits for it, woken by
// NotifyScanned or, failing that, by its own backoff.
func (r *autoRun) awaitScan(ctx context.Context) bool {
	s, o := r.s, r.s.auto.opts
	scan, err := s.scanRepo.GetLatestByComponent(ctx, r.comp.ID)
	if err == nil && scan != nil && !scan.ScannedAt.Before(r.e.LastPublishedAt) {
		if scan.Status == domain.ScanStatusFailed {
			// A scanner that errored found nothing because it looked at
			// nothing. Nobody is watching an automatic promotion go through,
			// so it fails closed rather than read that as clean.
			r.blocked(ctx, fmt.Errorf("rule %q requires a scan, and the scan of this publish failed: %s",
				r.rule.Name, scan.Error))
			return true
		}
		return false
	}
	now := o.Now()
	if now.Sub(r.e.LastPublishedAt) >= o.ScanWait {
		r.blocked(ctx, fmt.Errorf("rule %q requires a scan, but no scan of this publish arrived within %s — "+
			"check that scanning is enabled and that a scanner covers %s components", r.rule.Name, o.ScanWait, r.comp.Format))
		return true
	}
	r.retry(ctx, now.Add(autoBackoff(r.e.Attempts)), true, "waiting for a scan")
	return true
}

// copyNow runs the copy for the (pending) automatic request under its row lock,
// the way an auto-approved Promote does.
func (r *autoRun) copyNow(ctx context.Context, req *domain.PromotionRequest) {
	s := r.s
	var copyErr error
	var included int
	lockErr := s.promotionRepo.WithPendingRequestLock(ctx, req.ID,
		func(ctx context.Context, locked *domain.PromotionRequest) repository.PromotionOutcome {
			now := time.Now()
			freshRule, rerr := s.promotionRepo.GetRule(ctx, locked.RuleID)
			if rerr != nil || freshRule == nil {
				copyErr = fmt.Errorf("promotion rule not found: %s", locked.RuleID)
				return repository.PromotionOutcome{Status: domain.PromotionFailed, CompletedAt: &now, Error: copyErr.Error()}
			}
			included, copyErr = s.executeCopy(ctx, locked, freshRule)
			if copyErr != nil {
				return repository.PromotionOutcome{Status: domain.PromotionFailed, CompletedAt: &now, Error: copyErr.Error()}
			}
			return repository.PromotionOutcome{Status: domain.PromotionCompleted, CompletedAt: &now}
		})
	if lockErr != nil {
		// Someone settled it first — a reviewer, or another replica.
		return
	}
	extra := map[string]any{"request_id": req.ID, "included_components": included}
	if copyErr != nil {
		extra["reason"] = copyErr.Error()
		r.auditEvent(ctx, AuditAutoPromoteBlocked, "failure", extra)
		s.auto.log.Warnw("auto-promotion: copy failed", "rule", r.rule.Name, "component", r.comp.ID, "err", copyErr)
		return
	}
	r.auditEvent(ctx, AuditAutoPromoteCopied, "success", extra)
}

// blocked records a refusal that is final for this publish: a failed automatic
// request carrying the reason (what the Promotion requests list shows), an
// audit event, and the row removed. A later publish into the component queues
// it afresh.
func (r *autoRun) blocked(ctx context.Context, reason error) {
	s := r.s
	now := time.Now()
	req := &domain.PromotionRequest{
		RuleID:      r.e.RuleID,
		ComponentID: r.e.ComponentID,
		Status:      domain.PromotionFailed,
		CompletedAt: &now,
		Error:       "automatic promotion blocked: " + reason.Error(),
	}
	extra := map[string]any{"reason": reason.Error()}
	if _, err := s.promotionRepo.CreateAutoRequest(ctx, req); err != nil {
		s.auto.log.Warnw("auto-promotion: blocked request not recorded", "rule", r.e.RuleID, "component", r.e.ComponentID, "err", err)
	} else {
		extra["request_id"] = req.ID
	}
	r.auditEvent(ctx, AuditAutoPromoteBlocked, "failure", extra)
	s.auto.log.Infow("auto-promotion blocked", "rule", r.e.RuleID, "component", r.e.ComponentID, "reason", reason.Error())
	r.finish(ctx)
}

// retryTransient backs off an evaluation that failed on something that may
// clear up by itself, and gives up — as blocked — after MaxAttempts.
func (r *autoRun) retryTransient(ctx context.Context, err error) {
	if r.e.Attempts+1 >= r.s.auto.opts.MaxAttempts {
		r.blocked(ctx, fmt.Errorf("giving up after %d attempts: %w", r.e.Attempts+1, err))
		return
	}
	r.s.auto.log.Warnw("auto-promotion: will retry", "rule", r.e.RuleID, "component", r.e.ComponentID, "err", err)
	r.retry(ctx, r.s.auto.opts.Now().Add(autoBackoff(r.e.Attempts)), false, err.Error())
}

func (r *autoRun) retry(ctx context.Context, due time.Time, waitingForScan bool, reason string) {
	if err := r.s.auto.queue.Retry(ctx, r.e.ID, r.e.Generation, due, waitingForScan, reason); err != nil {
		// The lease runs out and the row comes back by itself.
		r.s.auto.log.Warnw("auto-promotion: reschedule failed", "entry", r.e.ID, "err", err)
	}
}

func (r *autoRun) finish(ctx context.Context) {
	if err := r.s.auto.queue.Finish(ctx, r.e.ID, r.e.Generation); err != nil {
		r.s.auto.log.Warnw("auto-promotion: finish failed", "entry", r.e.ID, "err", err)
	}
}

// auditEvent writes one auto-promotion audit event. Best-effort: the request
// row is the record that matters, the audit trail is its echo.
func (r *autoRun) auditEvent(ctx context.Context, action, result string, extra map[string]any) {
	a := r.s.auto.audit
	if a == nil {
		return
	}
	c := map[string]any{"rule_id": r.e.RuleID, "automatic": true}
	name := r.e.ComponentID
	if r.rule != nil {
		c["rule"] = r.rule.Name
		c["from_repo"] = r.rule.FromRepo
		c["to_repo"] = r.rule.ToRepo
	}
	if r.comp != nil {
		name = componentLabel(r.comp)
	}
	for k, v := range extra {
		c[k] = v
	}
	if err := a.Write(ctx, &domain.AuditEvent{
		EventTime:  time.Now(),
		Username:   autoPromotionAuditActor,
		Domain:     AuditDomainPromotion,
		Action:     action,
		EntityType: autoPromotionAuditSubject,
		EntityID:   r.e.ComponentID,
		EntityName: name,
		Context:    c,
		Result:     result,
	}); err != nil {
		r.s.auto.log.Warnw("auto-promotion: audit event not written", "action", action, "err", err)
	}
}

// componentLabel is "group:name:version" (or "name:version"), for audit rows.
func componentLabel(c *domain.Component) string {
	label := c.Name + ":" + c.Version
	if c.Group != "" {
		label = c.Group + ":" + label
	}
	return label
}

// autoBackoff is autoRetryBase doubled per attempt, capped at autoRetryMax.
func autoBackoff(attempts int) time.Duration {
	d := autoRetryBase
	for i := 0; i < attempts && d < autoRetryMax; i++ {
		d *= 2
	}
	if d > autoRetryMax {
		d = autoRetryMax
	}
	return d
}

// lookupMissing reports a lookup that found nothing, however the repository
// spells it.
func lookupMissing[T any](v *T, err error) bool {
	return errors.Is(err, repository.ErrNotFound) || (err == nil && v == nil)
}
