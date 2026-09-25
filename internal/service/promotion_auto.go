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
// HA: rows are claimed one at a time with FOR UPDATE SKIP LOCKED, a lease and
// a claim token, so replicas share the queue without a distributed lock, a
// crashed node's row is picked up once its lease runs out, and a node that
// outlived its lease cannot finish or reschedule a row someone else now holds.
// The copy itself runs under the request's
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

	// scanner answers whether a format can be scanned at all; nil means the
	// question is not asked and a scan gate simply waits.
	scanner AutoScanner
	// retrigger re-queues the scan a row waits for on every re-check.
	retrigger bool
}

// AutoScanner is what auto-promotion needs from the scan service: whether a
// scan of a format can happen at all, and a way to ask for one again. The
// automatic scan queue is in memory and bounded — a trigger is dropped when
// it is full and lost on restart — so a row waiting for a scan re-asks on each
// re-check instead of trusting the upload's one trigger. *ScanService
// satisfies it; its TriggerAsync ignores an id already queued.
type AutoScanner interface {
	TriggerAsync(componentID string)
	CoversFormat(ctx context.Context, format string) (ok bool, why string)
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

// WithAutoPromotionScanner lets a rule's scan gate fail fast on a format no
// scanner covers, and — with retrigger, which needs automatic scanning on —
// re-queue the scan it waits for. Call it after WithAutoPromotion.
func (s *PromotionService) WithAutoPromotionScanner(sc AutoScanner, retrigger bool) *PromotionService {
	if s.auto != nil {
		s.auto.scanner = sc
		s.auto.retrigger = retrigger
	}
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
	if _, err := s.auto.queue.EnqueuePublish(ctx, repoName, componentID, s.auto.opts.Now(), s.auto.opts.SettleWindow); err != nil {
		s.auto.log.Warnw("auto-promotion: publish not recorded", "repository", repoName, "component", componentID, "err", err)
	}
}

// NotifyScanned wakes rows waiting for a scan of componentID; the scan service
// calls it when it stores a result. Without it the worker still finds the
// scan on its next re-check — this only makes it prompt.
func (s *PromotionService) NotifyScanned(ctx context.Context, componentID string) {
	if s.auto == nil {
		return
	}
	if err := s.auto.queue.WakeForScan(ctx, componentID); err != nil {
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

// ProcessAutoPromotions evaluates up to BatchSize due rows and returns how
// many it took. One pass of RunAutoPromotion.
//
// Rows are claimed one at a time, each just before it is evaluated: a lease
// taken for a whole batch would run down while earlier rows copy, and a slow
// copy would hand the rest of the batch to another replica while this one
// still meant to work it.
func (s *PromotionService) ProcessAutoPromotions(ctx context.Context) (int, error) {
	if s.auto == nil {
		return 0, nil
	}
	o := s.auto.opts
	n := 0
	for n < o.BatchSize && ctx.Err() == nil {
		entries, err := s.auto.queue.Claim(ctx, o.Lease, 1)
		if err != nil {
			return n, err
		}
		if len(entries) == 0 {
			break
		}
		n++
		s.processAutoEntry(ctx, entries[0])
	}
	return n, nil
}

// autoRun is one evaluation of one queue row.
type autoRun struct {
	s    *PromotionService
	e    domain.AutoPromotionEntry
	rule *domain.PromotionRule
	comp *domain.Component
	// started is whether AUTO_PROMOTE_STARTED has been audited for this
	// generation (by this run or an earlier one).
	started bool
}

func (s *PromotionService) processAutoEntry(ctx context.Context, e domain.AutoPromotionEntry) {
	r := &autoRun{s: s, e: e, started: e.Started}
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

	// Once per publish, whatever this evaluation goes on to do — reschedule
	// for the settle window, wait for a scan, or copy.
	if !r.started {
		r.auditEvent(ctx, AuditAutoPromoteStarted, "success", nil)
		r.started = true
	}

	// The window may have been lengthened since the row was queued.
	if settled := e.LastPublishedAt.Add(o.SettleWindow); o.Now().Before(settled) {
		r.retry(ctx, settled.Sub(o.Now()), false, false, "settling")
		return
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

	published := e.LastPublishedAt
	req := &domain.PromotionRequest{
		RuleID:             rule.ID,
		ComponentID:        comp.ID,
		Status:             domain.PromotionPending,
		PublishedAt:        &published,
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
// content is still unscanned. Both timestamps come from application clocks —
// the scanning process stamps scanned_at when it starts, the publishing one
// stamps last_published_at — which is why the queue keeps last_published_at
// on that clock rather than the database's.
//
// A format no scanner covers is refused at once rather than after ScanWait.
// Otherwise the scan the upload queued is waited for, re-asked for on every
// re-check (the automatic queue is lossy), and woken early by NotifyScanned.
func (r *autoRun) awaitScan(ctx context.Context) bool {
	s, o := r.s, r.s.auto.opts
	scan, err := s.scanRepo.GetLatestByComponent(ctx, r.comp.ID)
	if err == nil && scan != nil && !scan.ScannedAt.Before(r.e.LastPublishedAt) {
		if scan.Status == domain.ScanStatusFailed {
			// A scanner that errored found nothing because it looked at
			// nothing. Nobody is watching an automatic promotion go through,
			// so it fails closed rather than read that as clean. (A manual
			// Promote's scanGate still passes such a scan — it counts
			// findings, and an errored scan has none; a reviewer is there to
			// look.)
			r.blocked(ctx, fmt.Errorf("rule %q requires a scan, and the scan of this publish failed: %s",
				r.rule.Name, scan.Error))
			return true
		}
		return false
	}
	if sc := s.auto.scanner; sc != nil {
		if ok, why := sc.CoversFormat(ctx, r.comp.Format); !ok {
			r.blocked(ctx, fmt.Errorf("rule %q requires a scan, which can never pass here: %s", r.rule.Name, why))
			return true
		}
	}
	elapsed := o.Now().Sub(r.e.LastPublishedAt)
	if elapsed >= o.ScanWait {
		r.blocked(ctx, fmt.Errorf("rule %q requires a scan, but no scan of this publish arrived within %s — "+
			"check that scanning is enabled", r.rule.Name, o.ScanWait))
		return true
	}
	if sc := s.auto.scanner; sc != nil && s.auto.retrigger {
		sc.TriggerAsync(r.comp.ID)
	}
	r.retry(ctx, scanRecheck(elapsed), true, false, "waiting for a scan")
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

// blocked records a refusal that is final for this publish, and removes the
// row; a later publish into the component queues it afresh.
//
// The record is a failed automatic request carrying the reason — what the
// Promotion requests list shows — plus an audit event. When an automatic
// request for the pair is still pending from an earlier publish, that request
// is the one failed: it would otherwise stay approvable, and Approve copies
// the component as it is now — the content this publish was just refused for.
func (r *autoRun) blocked(ctx context.Context, reason error) {
	s := r.s
	msg := "automatic promotion blocked: " + reason.Error()
	extra := map[string]any{"reason": reason.Error()}
	superseded, ferr := s.promotionRepo.FailPendingAutoRequests(ctx, r.e.RuleID, r.e.ComponentID, msg)
	if ferr != nil {
		s.auto.log.Warnw("auto-promotion: pending request not failed", "rule", r.e.RuleID, "component", r.e.ComponentID, "err", ferr)
	}
	if superseded > 0 {
		extra["superseded_pending"] = superseded
	} else {
		now := time.Now()
		published := r.e.LastPublishedAt
		req := &domain.PromotionRequest{
			RuleID:      r.e.RuleID,
			ComponentID: r.e.ComponentID,
			Status:      domain.PromotionFailed,
			CompletedAt: &now,
			PublishedAt: &published,
			Error:       msg,
		}
		if _, err := s.promotionRepo.CreateAutoRequest(ctx, req); err != nil {
			s.auto.log.Warnw("auto-promotion: blocked request not recorded", "rule", r.e.RuleID, "component", r.e.ComponentID, "err", err)
		} else {
			extra["request_id"] = req.ID
		}
	}
	r.auditEvent(ctx, AuditAutoPromoteBlocked, "failure", extra)
	s.auto.log.Infow("auto-promotion blocked", "rule", r.e.RuleID, "component", r.e.ComponentID, "reason", reason.Error())
	r.finish(ctx)
}

// retryTransient backs off an evaluation that failed on something that may
// clear up by itself, and gives up — as blocked — after MaxAttempts. Only
// these failures count as attempts; waiting is not failing.
func (r *autoRun) retryTransient(ctx context.Context, err error) {
	if r.e.Attempts+1 >= r.s.auto.opts.MaxAttempts {
		r.blocked(ctx, fmt.Errorf("giving up after %d attempts: %w", r.e.Attempts+1, err))
		return
	}
	r.s.auto.log.Warnw("auto-promotion: will retry", "rule", r.e.RuleID, "component", r.e.ComponentID, "err", err)
	r.retry(ctx, autoBackoff(r.e.Attempts), false, true, err.Error())
}

func (r *autoRun) retry(ctx context.Context, dueIn time.Duration, waitingForScan, countAttempt bool, reason string) {
	if err := r.s.auto.queue.Retry(ctx, r.e, repository.AutoPromotionRetry{
		DueIn:          dueIn,
		WaitingForScan: waitingForScan,
		Started:        r.started,
		CountAttempt:   countAttempt,
		Reason:         reason,
	}); err != nil {
		// The lease runs out and the row comes back by itself.
		r.s.auto.log.Warnw("auto-promotion: reschedule failed", "entry", r.e.ID, "err", err)
	}
}

func (r *autoRun) finish(ctx context.Context) {
	if err := r.s.auto.queue.Finish(ctx, r.e); err != nil {
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

// scanRecheck paces the re-checks of a row waiting for a scan by how long it
// has waited — often at first, when the scan the upload queued is most likely
// to land, then less so — within autoRetryBase..autoRetryMax. It does not use
// the attempts counter: waiting is not failing.
func scanRecheck(waited time.Duration) time.Duration {
	d := waited / 2
	if d < autoRetryBase {
		d = autoRetryBase
	}
	if d > autoRetryMax {
		d = autoRetryMax
	}
	return d
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
