package service_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/formats/base"
	"github.com/nexspence-oss/nexspence/internal/service"
)

// fakeAutoScanner records re-triggers and answers CoversFormat from a set.
type fakeAutoScanner struct {
	mu       sync.Mutex
	covers   map[string]bool
	triggers []string
}

func (f *fakeAutoScanner) TriggerAsync(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.triggers = append(f.triggers, id)
}

func (f *fakeAutoScanner) CoversFormat(_ context.Context, format string) (bool, string) {
	if f.covers[format] {
		return true, ""
	}
	return false, "no scanner covers " + format + " components"
}

func (f *fakeAutoScanner) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.triggers)
}

func pendingRequests(t *testing.T, f *autoFixture) []domain.PromotionRequest {
	t.Helper()
	reqs, _ := f.promo.ListRequests(context.Background(), string(domain.PromotionPending))
	return reqs
}

// A later publish blocked by its scan fails the request still pending from the
// earlier, clean publish: approving it would copy the vulnerable content.
func TestAutoPromotion_BlockedPublishSupersedesPendingRequest(t *testing.T) {
	f := newAutoFixture(t, "maven2", domain.PromotionRule{AutoPromote: true, RequireScanPass: true,
		RequireManualApproval: true})
	a := f.publish(jarPath, mavenLib, "v1")
	f.scan(a.ComponentID, nil, f.clock())
	f.advance(testSettle)
	f.run()
	pending := pendingRequests(t, f)
	if len(pending) != 1 {
		t.Fatalf("pending = %+v", pending)
	}

	f.advance(time.Minute)
	f.publish(jarPath, mavenLib, "v2")
	f.scan(a.ComponentID, map[string]int{"critical": 1}, f.clock())
	f.advance(testSettle)
	f.run()

	reqs := f.requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %+v, want the one request, failed in place", reqs)
	}
	if reqs[0].Status != domain.PromotionFailed || !strings.Contains(reqs[0].Error, "1 critical") {
		t.Fatalf("request = %+v, want failed with the scan findings", reqs[0])
	}
	if err := f.svc.Approve(context.Background(), pending[0].ID, "reviewer"); err == nil {
		t.Fatal("the superseded request was approved")
	}
	if f.targetHas(jarPath) {
		t.Fatal("vulnerable content copied")
	}
}

// Approve of an automatic request waits for the worker to evaluate a newer
// publish, and for a scan of it; then it copies.
func TestAutoPromotion_ApproveWaitsForNewerPublishAndItsScan(t *testing.T) {
	f := newAutoFixture(t, "maven2", domain.PromotionRule{AutoPromote: true, RequireScanPass: true,
		RequireManualApproval: true})
	ctx := context.Background()
	a := f.publish(jarPath, mavenLib, "v1")
	f.scan(a.ComponentID, nil, f.clock())
	f.advance(testSettle)
	f.run()
	req := pendingRequests(t, f)[0]

	f.advance(time.Minute)
	f.publish(jarPath, mavenLib, "v2") // queued, not yet evaluated
	err := f.svc.Approve(ctx, req.ID, "reviewer")
	if err == nil || !strings.Contains(err.Error(), "published again") {
		t.Fatalf("Approve = %v, want held for the re-evaluation", err)
	}
	if got := pendingRequests(t, f); len(got) != 1 {
		t.Fatalf("a held approval settled the request: %+v", f.requests())
	}

	f.advance(testSettle)
	f.run() // waits for a scan of v2
	if err := f.svc.Approve(ctx, req.ID, "reviewer"); err == nil {
		t.Fatal("approved while the new content waits for its scan")
	}

	f.advance(time.Second)
	f.scan(a.ComponentID, nil, f.clock())
	f.svc.NotifyScanned(ctx, a.ComponentID)
	f.run() // confirms the pending request for v2
	if err := f.svc.Approve(ctx, req.ID, "reviewer"); err != nil {
		t.Fatalf("Approve after a clean scan of v2: %v", err)
	}
	if got := f.targetBody(jarPath); got != "v2" {
		t.Fatalf("target = %q", got)
	}
}

// Without a queued row, an automatic request still needs a scan that started
// after the publish it was evaluated for, and one that did not error.
func TestAutoPromotion_ApproveChecksScanFreshness(t *testing.T) {
	f := newAutoFixture(t, "maven2", domain.PromotionRule{AutoPromote: true, RequireScanPass: true,
		RequireManualApproval: true})
	ctx := context.Background()
	a := f.publish(jarPath, mavenLib, "v1")
	// Get the queue row out of the way (the rule stops auto-promoting, so the
	// worker drops it), then file the request by hand for a publish later than
	// any scan.
	f.advance(testSettle)
	f.run()                    // waits for a scan
	f.rule.AutoPromote = false // the worker drops the row
	if err := f.svc.UpdateRule(ctx, f.rule); err != nil {
		t.Fatal(err)
	}
	f.advance(time.Hour)
	f.run()
	for _, e := range f.queued() {
		t.Fatalf("row left behind: %+v", e)
	}
	published := f.clock()
	req := &domain.PromotionRequest{RuleID: f.rule.ID, ComponentID: a.ComponentID,
		Status: domain.PromotionPending, PublishedAt: &published}
	if _, err := f.promo.CreateAutoRequest(ctx, req); err != nil {
		t.Fatal(err)
	}
	f.scan(a.ComponentID, nil, published.Add(-time.Second))
	if err := f.svc.Approve(ctx, req.ID, "r"); err == nil || !strings.Contains(err.Error(), "no scan of its current content") {
		t.Fatalf("Approve = %v", err)
	}
	if err := f.scans.Insert(ctx, &domain.ScanResultRow{ComponentID: a.ComponentID, Status: domain.ScanStatusFailed,
		Error: "osv down", ScannedAt: published.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Approve(ctx, req.ID, "r"); err == nil || !strings.Contains(err.Error(), "osv down") {
		t.Fatalf("Approve = %v", err)
	}
	f.scan(a.ComponentID, nil, published.Add(2*time.Second))
	if err := f.svc.Approve(ctx, req.ID, "r"); err != nil {
		t.Fatalf("Approve: %v", err)
	}
}

// A manual request is re-gated at approval too: a scan that arrived while it
// sat pending decides.
func TestPromotion_ApproveRerunsScanGateForManualRequests(t *testing.T) {
	f := newAutoFixture(t, "maven2", domain.PromotionRule{RequireScanPass: true, RequireManualApproval: true})
	ctx := context.Background()
	a := f.publish(jarPath, mavenLib, "v1")
	f.scan(a.ComponentID, nil, f.clock())
	reqs, err := f.svc.Promote(ctx, f.rule.ID, []string{a.ComponentID}, "user-1")
	if err != nil {
		t.Fatal(err)
	}
	f.advance(time.Minute)
	f.scan(a.ComponentID, map[string]int{"high": 2}, f.clock())
	if err := f.svc.Approve(ctx, reqs[0].ID, "r"); err == nil || !strings.Contains(err.Error(), "2 high") {
		t.Fatalf("Approve = %v, want the new findings", err)
	}
	if f.targetHas(jarPath) {
		t.Fatal("copied despite the findings")
	}
}

// maven-metadata.xml neither starts a promotion nor travels with one: the
// target generates its own from what it holds.
func TestAutoPromotion_MavenMetadataIgnored(t *testing.T) {
	f := newAutoFixture(t, "maven2", domain.PromotionRule{AutoPromote: true})
	// What mvn deploy uploads after the jar: the artifact-level index, which
	// maven's path parsing turns into a bogus component.
	f.publish("/com/example/lib/maven-metadata.xml", base.Coords{Group: "com.example", Name: "lib", Version: "maven-metadata.xml"}, "<metadata/>")
	if q := f.queued(); len(q) != 0 {
		t.Fatalf("metadata upload queued: %+v", q)
	}
	f.publish(jarPath, mavenLib, "jar")
	f.publish("/com/example/lib/1.0.0/maven-metadata.xml", mavenLib, "<metadata/>")
	f.advance(testSettle)
	f.run()
	if !f.targetHas(jarPath) {
		t.Fatal("jar not promoted")
	}
	if f.targetHas("/com/example/lib/1.0.0/maven-metadata.xml") {
		t.Fatal("a literal maven-metadata.xml was copied into the target")
	}
}

// Rows are claimed one at a time, each just before it is worked.
func TestAutoPromotion_ProcessClaimsUpToBatchOneAtATime(t *testing.T) {
	f := newAutoFixture(t, "raw", domain.PromotionRule{AutoPromote: true})
	f.svc = mustPromotionSvc(t, f).WithAutoPromotion(f.queue, f.audit, nil,
		service.AutoPromotionOptions{SettleWindow: testSettle, BatchSize: 3, Now: f.clock})
	f.deps.Publishes = f.svc
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		f.publish("/"+n, base.Coords{Name: n}, n)
	}
	f.advance(testSettle)
	if n := f.run(); n != 3 {
		t.Fatalf("processed %d, want the batch of 3", n)
	}
	if n := f.run(); n != 2 {
		t.Fatalf("processed %d, want the remaining 2", n)
	}
}

// A scan gate on a format no scanner covers fails at once, not after the wait.
func TestAutoPromotion_UnscannableFormatBlockedAtOnce(t *testing.T) {
	f := newAutoFixture(t, "raw", domain.PromotionRule{AutoPromote: true, RequireScanPass: true})
	f.svc.WithAutoPromotionScanner(&fakeAutoScanner{covers: map[string]bool{"maven2": true}}, true)
	f.publish("/app.zip", base.Coords{Name: "app.zip"}, "zip")
	f.advance(testSettle)
	f.run()
	reqs := f.requests()
	if len(reqs) != 1 || !strings.Contains(reqs[0].Error, "no scanner covers raw components") {
		t.Fatalf("requests = %+v", reqs)
	}
}

// Waiting for a scan re-asks for it on every re-check, and waiting is not
// failing: the attempts counter stays at zero.
func TestAutoPromotion_ScanWaitRetriggersWithoutCountingAttempts(t *testing.T) {
	f := newAutoFixture(t, "maven2", domain.PromotionRule{AutoPromote: true, RequireScanPass: true})
	sc := &fakeAutoScanner{covers: map[string]bool{"maven2": true}}
	f.svc.WithAutoPromotionScanner(sc, true)
	a := f.publish(jarPath, mavenLib, "jar")
	f.advance(testSettle)
	for i := 0; i < 4; i++ {
		f.run()
		f.advance(3 * time.Minute)
	}
	if sc.count() < 3 {
		t.Fatalf("re-triggered %d times", sc.count())
	}
	q := f.queued()
	if len(q) != 1 || q[0].Attempts != 0 || !q[0].WaitingForScan {
		t.Fatalf("queue = %+v", q)
	}
	if acts := strings.Join(f.auditActions(), ","); acts != "AUTO_PROMOTE_STARTED" {
		t.Fatalf("audit = %s, want one start for the publish", acts)
	}
	f.scan(a.ComponentID, nil, f.clock())
	f.svc.NotifyScanned(context.Background(), a.ComponentID)
	f.run()
	if !f.targetHas(jarPath) {
		t.Fatal("not promoted after the scan")
	}
}

// The start is audited on the evaluation that finds the row still settling.
func TestAutoPromotion_StartedAuditedWhileSettling(t *testing.T) {
	f := newAutoFixture(t, "maven2", domain.PromotionRule{AutoPromote: true})
	f.publish(jarPath, mavenLib, "jar")
	f.svc = mustPromotionSvc(t, f).WithAutoPromotion(f.queue, f.audit, nil,
		service.AutoPromotionOptions{SettleWindow: 2 * testSettle, Now: f.clock})
	f.advance(testSettle)
	f.run()
	if acts := f.auditActions(); len(acts) != 1 || acts[0] != "AUTO_PROMOTE_STARTED" {
		t.Fatalf("audit = %v", acts)
	}
	f.advance(testSettle)
	f.run()
	if acts := strings.Join(f.auditActions(), ","); acts != "AUTO_PROMOTE_STARTED,AUTO_PROMOTE_COPIED" {
		t.Fatalf("audit = %s", acts)
	}
}

// A publish stamped by a replica whose clock lags does not move the recorded
// publish time back.
func TestAutoPromotion_PublishTimeOnlyMovesForward(t *testing.T) {
	f := newAutoFixture(t, "maven2", domain.PromotionRule{AutoPromote: true})
	a := f.publish(jarPath, mavenLib, "jar")
	first := f.queued()[0].LastPublishedAt
	if _, err := f.queue.EnqueuePublish(context.Background(), f.from.Name, a.ComponentID, first.Add(-time.Hour), time.Minute); err != nil {
		t.Fatal(err)
	}
	if got := f.queued()[0].LastPublishedAt; !got.Equal(first) {
		t.Fatalf("last_published_at moved back to %s", got)
	}
}

// A claim whose lease another worker has since taken over can neither finish
// nor reschedule the row.
func TestAutoPromotion_StaleClaimCannotTouchRow(t *testing.T) {
	f := newAutoFixture(t, "maven2", domain.PromotionRule{AutoPromote: true})
	f.publish(jarPath, mavenLib, "jar")
	ctx := context.Background()
	f.advance(testSettle)
	stale, _ := f.queue.Claim(ctx, time.Minute, 1)
	f.advance(2 * time.Minute) // lease gone
	fresh, _ := f.queue.Claim(ctx, time.Minute, 1)
	if len(stale) != 1 || len(fresh) != 1 || stale[0].ClaimToken == fresh[0].ClaimToken {
		t.Fatalf("claims = %+v / %+v", stale, fresh)
	}
	if err := f.queue.Finish(ctx, stale[0]); err != nil {
		t.Fatal(err)
	}
	if len(f.queued()) != 1 {
		t.Fatal("a stale Finish removed the row")
	}
	if err := f.queue.Finish(ctx, fresh[0]); err != nil {
		t.Fatal(err)
	}
	if len(f.queued()) != 0 {
		t.Fatal("the current claim could not finish")
	}
}
