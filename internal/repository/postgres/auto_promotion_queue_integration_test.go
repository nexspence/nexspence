//go:build integration

package postgres

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
	"github.com/nexspence-oss/nexspence/internal/testutil/pgtest"
)

// autoQueueFixture is an auto_promote rule, a manual rule on the same source,
// and a component in the source.
func autoQueueFixture(t *testing.T, ctx context.Context, suffix string) (p promotionParents, auto *domain.PromotionRule, compID string) {
	t.Helper()
	pool := pgtest.Pool(t)
	p = makePromotionParents(t, ctx, suffix)
	pr := NewPromotionRepo(pool)
	auto = &domain.PromotionRule{Name: "auto_" + suffix, FromRepo: p.FromRepo, ToRepo: p.ToRepo, AutoPromote: true}
	if err := pr.CreateRule(ctx, auto); err != nil {
		t.Fatal(err)
	}
	manual := &domain.PromotionRule{Name: "manual_" + suffix, FromRepo: p.FromRepo, ToRepo: p.ToRepo}
	if err := pr.CreateRule(ctx, manual); err != nil {
		t.Fatal(err)
	}
	return p, auto, makePromotionComponent(t, ctx, p.FromRepoID, "1.0-"+suffix)
}

// rowsFor lists the queue rows of one component (other tests share the table).
func rowsFor(t *testing.T, q *AutoPromotionQueueRepo, compID string) []domain.AutoPromotionEntry {
	t.Helper()
	all, err := q.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var out []domain.AutoPromotionEntry
	for _, e := range all {
		if e.ComponentID == compID {
			out = append(out, e)
		}
	}
	return out
}

// claimFor claims until it gets compID's row (or nothing is left), releasing
// any other test's rows it picks up on the way.
func claimFor(t *testing.T, q *AutoPromotionQueueRepo, compID string, lease time.Duration) *domain.AutoPromotionEntry {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < 100; i++ {
		got, err := q.Claim(ctx, lease, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) == 0 {
			return nil
		}
		if got[0].ComponentID == compID {
			return &got[0]
		}
		_ = q.Retry(ctx, got[0], repository.AutoPromotionRetry{Started: got[0].Started, WaitingForScan: got[0].WaitingForScan})
	}
	return nil
}

func TestAutoPromotionQueue_EnqueueClaimFinish(t *testing.T) {
	ctx := context.Background()
	q := NewAutoPromotionQueueRepo(pgtest.Pool(t))
	p, auto, comp := autoQueueFixture(t, ctx, "aq1")
	t0 := time.Now().UTC().Truncate(time.Microsecond)

	// Only the auto_promote rule gets a row.
	n, err := q.EnqueuePublish(ctx, p.FromRepo, comp, t0, time.Hour)
	if err != nil || n != 1 {
		t.Fatalf("EnqueuePublish = %d, %v", n, err)
	}
	if n, _ := q.EnqueuePublish(ctx, "no-such-repo", comp, t0, 0); n != 0 {
		t.Fatalf("a repository without auto rules queued %d rows", n)
	}
	if e := claimFor(t, q, comp, time.Minute); e != nil {
		t.Fatal("claimed a row before it was due")
	}
	// A second publish refreshes it; an earlier publish time does not win.
	if _, err := q.EnqueuePublish(ctx, p.FromRepo, comp, t0.Add(-time.Hour), 0); err != nil {
		t.Fatal(err)
	}
	e := claimFor(t, q, comp, time.Minute)
	if e == nil {
		t.Fatal("not claimable once due")
	}
	if e.RuleID != auto.ID || e.Generation != 2 || !e.LastPublishedAt.Equal(t0) || e.ClaimToken == "" {
		t.Fatalf("entry = %+v", e)
	}
	// Leased: nobody else gets it.
	if again := claimFor(t, q, comp, time.Minute); again != nil {
		t.Fatal("a leased row was claimed twice")
	}

	// A publish during the evaluation survives the evaluator's Finish.
	if _, err := q.EnqueuePublish(ctx, p.FromRepo, comp, t0.Add(time.Minute), 0); err != nil {
		t.Fatal(err)
	}
	if err := q.Finish(ctx, *e); err != nil {
		t.Fatal(err)
	}
	rows := rowsFor(t, q, comp)
	if len(rows) != 1 || rows[0].Generation != 3 || rows[0].ClaimToken != "" {
		t.Fatalf("after a stale Finish: %+v", rows)
	}
	e = claimFor(t, q, comp, time.Minute)
	if e == nil {
		t.Fatal("the refreshed row was not claimable")
	}
	if err := q.Finish(ctx, *e); err != nil {
		t.Fatal(err)
	}
	if rows := rowsFor(t, q, comp); len(rows) != 0 {
		t.Fatalf("Finish left %+v", rows)
	}
}

// A worker whose lease ran out and whose row someone else claimed can neither
// finish nor reschedule it.
func TestAutoPromotionQueue_StaleClaimIgnored(t *testing.T) {
	ctx := context.Background()
	q := NewAutoPromotionQueueRepo(pgtest.Pool(t))
	p, _, comp := autoQueueFixture(t, ctx, "aq5")
	if _, err := q.EnqueuePublish(ctx, p.FromRepo, comp, time.Now(), 0); err != nil {
		t.Fatal(err)
	}
	stale := claimFor(t, q, comp, time.Millisecond)
	if stale == nil {
		t.Fatal("not claimed")
	}
	time.Sleep(20 * time.Millisecond)
	fresh := claimFor(t, q, comp, time.Hour)
	if fresh == nil || fresh.ClaimToken == stale.ClaimToken {
		t.Fatalf("reclaim = %+v", fresh)
	}
	if err := q.Retry(ctx, *stale, repository.AutoPromotionRetry{DueIn: 10 * time.Hour, Reason: "stale"}); err != nil {
		t.Fatal(err)
	}
	if err := q.Finish(ctx, *stale); err != nil {
		t.Fatal(err)
	}
	rows := rowsFor(t, q, comp)
	if len(rows) != 1 || rows[0].Reason == "stale" || rows[0].ClaimToken != fresh.ClaimToken {
		t.Fatalf("a stale claim changed the row: %+v", rows)
	}
	if err := q.Finish(ctx, *fresh); err != nil {
		t.Fatal(err)
	}
}

func TestAutoPromotionQueue_RetryAndWake(t *testing.T) {
	ctx := context.Background()
	q := NewAutoPromotionQueueRepo(pgtest.Pool(t))
	p, auto, comp := autoQueueFixture(t, ctx, "aq2")
	t0 := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := q.EnqueuePublish(ctx, p.FromRepo, comp, t0, 0); err != nil {
		t.Fatal(err)
	}
	if queued, err := q.Queued(ctx, auto.ID, comp); err != nil || !queued {
		t.Fatalf("Queued = %v, %v", queued, err)
	}
	e := claimFor(t, q, comp, time.Minute)
	if e == nil {
		t.Fatal("not claimed")
	}
	if err := q.Retry(ctx, *e, repository.AutoPromotionRetry{DueIn: time.Hour, WaitingForScan: true,
		Started: true, Reason: "waiting for a scan"}); err != nil {
		t.Fatal(err)
	}
	rows := rowsFor(t, q, comp)
	if len(rows) != 1 || rows[0].Attempts != 0 || !rows[0].WaitingForScan || !rows[0].Started ||
		rows[0].Reason != "waiting for a scan" {
		t.Fatalf("after Retry: %+v", rows)
	}
	if again := claimFor(t, q, comp, time.Minute); again != nil {
		t.Fatal("a rescheduled row was claimed early")
	}
	if err := q.WakeForScan(ctx, comp); err != nil {
		t.Fatal(err)
	}
	e = claimFor(t, q, comp, time.Minute)
	if e == nil {
		t.Fatal("WakeForScan did not make the row due")
	}
	if err := q.Retry(ctx, *e, repository.AutoPromotionRetry{DueIn: 0, CountAttempt: true, Reason: "db down"}); err != nil {
		t.Fatal(err)
	}
	if rows := rowsFor(t, q, comp); rows[0].Attempts != 1 {
		t.Fatalf("a transient failure was not counted: %+v", rows)
	}

	// A Retry for a generation a publish has replaced keeps the publish's state.
	e = claimFor(t, q, comp, time.Minute)
	if _, err := q.EnqueuePublish(ctx, p.FromRepo, comp, t0.Add(time.Minute), time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := q.Retry(ctx, *e, repository.AutoPromotionRetry{DueIn: 0, WaitingForScan: true, Reason: "stale"}); err != nil {
		t.Fatal(err)
	}
	rows = rowsFor(t, q, comp)
	if len(rows) != 1 || rows[0].WaitingForScan || rows[0].Attempts != 0 || rows[0].Started ||
		!rows[0].DueAt.After(time.Now().Add(30*time.Minute)) {
		t.Fatalf("stale Retry overrode the publish: %+v", rows)
	}
	// A component deleted takes its rows with it.
	if _, err := pgtest.Pool(t).Exec(ctx, `DELETE FROM components WHERE id = $1`, comp); err != nil {
		t.Fatal(err)
	}
	if rows := rowsFor(t, q, comp); len(rows) != 0 {
		t.Fatalf("rows of a deleted component survive: %+v", rows)
	}
	if queued, _ := q.Queued(ctx, auto.ID, comp); queued {
		t.Fatal("Queued after the row went")
	}
}

// Replicas claiming at the same instant never share a row (SKIP LOCKED).
func TestAutoPromotionQueue_ConcurrentClaimsAreDisjoint(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	q := NewAutoPromotionQueueRepo(pool)
	p, _, _ := autoQueueFixture(t, ctx, "aq3")
	ours := map[string]bool{}
	for i := 0; i < 40; i++ {
		c := makePromotionComponent(t, ctx, p.FromRepoID, fmt.Sprintf("c%02d", i))
		ours[c] = true
		if _, err := q.EnqueuePublish(ctx, p.FromRepo, c, time.Now(), 0); err != nil {
			t.Fatal(err)
		}
	}
	var mu sync.Mutex
	seen := map[string]int{}
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				got, err := q.Claim(ctx, time.Hour, 3)
				if err != nil || len(got) == 0 {
					return
				}
				mu.Lock()
				for _, e := range got {
					if ours[e.ComponentID] {
						seen[e.ID]++
					}
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(seen) != 40 {
		t.Fatalf("claimed %d distinct rows, want 40", len(seen))
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("row %s claimed %d times", id, n)
		}
	}
}

// One pending automatic request per (rule, component); settled and failed ones
// do not count, and manual requests are untouched by the rule.
func TestPromotionRepo_CreateAutoRequest(t *testing.T) {
	ctx := context.Background()
	pr := NewPromotionRepo(pgtest.Pool(t))
	p, auto, comp := autoQueueFixture(t, ctx, "aq4")

	t1 := time.Now().UTC().Truncate(time.Microsecond)
	first := &domain.PromotionRequest{RuleID: auto.ID, ComponentID: comp, Status: domain.PromotionPending, PublishedAt: &t1}
	created, err := pr.CreateAutoRequest(ctx, first)
	if err != nil || !created {
		t.Fatalf("first = %v, %v", created, err)
	}
	t2 := t1.Add(time.Minute)
	second := &domain.PromotionRequest{RuleID: auto.ID, ComponentID: comp, Status: domain.PromotionPending, PublishedAt: &t2}
	created, err = pr.CreateAutoRequest(ctx, second)
	if err != nil || created || second.ID != first.ID || !second.Automatic || !second.PublishedAt.Equal(t2) {
		t.Fatalf("second = %+v, %v, %v", second, created, err)
	}
	earlier := t1.Add(-time.Hour)
	third := &domain.PromotionRequest{RuleID: auto.ID, ComponentID: comp, Status: domain.PromotionPending, PublishedAt: &earlier}
	if _, err := pr.CreateAutoRequest(ctx, third); err != nil || !third.PublishedAt.Equal(t2) {
		t.Fatalf("published_at moved back: %+v, %v", third, err)
	}
	now := time.Now()
	failed := &domain.PromotionRequest{RuleID: auto.ID, ComponentID: comp, Status: domain.PromotionFailed,
		CompletedAt: &now, Error: "blocked"}
	if created, err := pr.CreateAutoRequest(ctx, failed); err != nil || !created {
		t.Fatalf("failed request = %v, %v", created, err)
	}
	manual := &domain.PromotionRequest{RuleID: auto.ID, ComponentID: comp, Status: domain.PromotionPending, RequestedBy: p.UserID}
	if err := pr.CreateRequest(ctx, manual); err != nil {
		t.Fatalf("a manual pending request beside an automatic one: %v", err)
	}

	got, err := pr.GetRequest(ctx, failed.ID)
	if err != nil || !got.Automatic || got.RequestedBy != "" || got.Error != "blocked" || got.CompletedAt == nil {
		t.Fatalf("stored failed request = %+v, %v", got, err)
	}
	got, _ = pr.GetRequest(ctx, manual.ID)
	if got.Automatic || got.RequestedBy != p.UserID {
		t.Fatalf("manual request = %+v", got)
	}

	// Failing the pending automatic request leaves the manual one alone.
	n, err := pr.FailPendingAutoRequests(ctx, auto.ID, comp, "superseded")
	if err != nil || n != 1 {
		t.Fatalf("FailPendingAutoRequests = %d, %v", n, err)
	}
	got, _ = pr.GetRequest(ctx, first.ID)
	if got.Status != domain.PromotionFailed || got.Error != "superseded" || got.CompletedAt == nil {
		t.Fatalf("superseded request = %+v", got)
	}
	if got, _ = pr.GetRequest(ctx, manual.ID); got.Status != domain.PromotionPending {
		t.Fatalf("manual request touched: %+v", got)
	}

	rule, _ := pr.GetRule(ctx, auto.ID)
	if !rule.AutoPromote {
		t.Fatal("auto_promote not stored")
	}
	rule.AutoPromote = false
	if err := pr.UpdateRule(ctx, rule); err != nil {
		t.Fatal(err)
	}
	if rule, _ = pr.GetRule(ctx, auto.ID); rule.AutoPromote {
		t.Fatal("auto_promote not updated")
	}
}
