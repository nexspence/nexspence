//go:build integration

package postgres

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/nexspence-oss/nexspence/internal/domain"
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

func TestAutoPromotionQueue_EnqueueClaimFinish(t *testing.T) {
	ctx := context.Background()
	q := NewAutoPromotionQueueRepo(pgtest.Pool(t))
	p, auto, comp := autoQueueFixture(t, ctx, "aq1")
	t0 := time.Now().UTC().Truncate(time.Microsecond)

	// Only the auto_promote rule gets a row, and a second publish refreshes it.
	n, err := q.EnqueuePublish(ctx, p.FromRepo, comp, t0, t0.Add(30*time.Second))
	if err != nil || n != 1 {
		t.Fatalf("EnqueuePublish = %d, %v", n, err)
	}
	if _, err := q.EnqueuePublish(ctx, p.FromRepo, comp, t0.Add(10*time.Second), t0.Add(40*time.Second)); err != nil {
		t.Fatal(err)
	}
	if n, _ := q.EnqueuePublish(ctx, "no-such-repo", comp, t0, t0); n != 0 {
		t.Fatalf("a repository without auto rules queued %d rows", n)
	}

	if got, _ := q.Claim(ctx, t0.Add(35*time.Second), time.Minute, 10); len(got) != 0 {
		t.Fatalf("claimed %d rows before they were due", len(got))
	}
	got, err := q.Claim(ctx, t0.Add(41*time.Second), time.Minute, 10)
	if err != nil || len(got) != 1 {
		t.Fatalf("Claim = %+v, %v", got, err)
	}
	e := got[0]
	if e.RuleID != auto.ID || e.ComponentID != comp || e.Generation != 2 || !e.LastPublishedAt.Equal(t0.Add(10*time.Second)) {
		t.Fatalf("entry = %+v", e)
	}
	// Leased: a second claimer gets nothing until the lease runs out.
	if again, _ := q.Claim(ctx, t0.Add(42*time.Second), time.Minute, 10); len(again) != 0 {
		t.Fatal("a leased row was claimed twice")
	}
	if again, _ := q.Claim(ctx, t0.Add(2*time.Minute), time.Minute, 10); len(again) != 1 {
		t.Fatal("an expired lease was not reclaimable")
	}

	// A publish during the evaluation survives the evaluator's Finish.
	if _, err := q.EnqueuePublish(ctx, p.FromRepo, comp, t0.Add(3*time.Minute), t0.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := q.Finish(ctx, e.ID, e.Generation); err != nil {
		t.Fatal(err)
	}
	all, _ := q.List(ctx)
	if len(all) != 1 || all[0].Generation != 3 {
		t.Fatalf("after a stale Finish: %+v", all)
	}
	got, _ = q.Claim(ctx, t0.Add(5*time.Minute), time.Minute, 10)
	if len(got) != 1 {
		t.Fatal("the refreshed row was not claimable")
	}
	if err := q.Finish(ctx, got[0].ID, got[0].Generation); err != nil {
		t.Fatal(err)
	}
	if all, _ := q.List(ctx); len(all) != 0 {
		t.Fatalf("Finish left %+v", all)
	}
}

func TestAutoPromotionQueue_RetryAndWake(t *testing.T) {
	ctx := context.Background()
	q := NewAutoPromotionQueueRepo(pgtest.Pool(t))
	p, _, comp := autoQueueFixture(t, ctx, "aq2")
	t0 := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := q.EnqueuePublish(ctx, p.FromRepo, comp, t0, t0); err != nil {
		t.Fatal(err)
	}
	got, _ := q.Claim(ctx, t0, time.Minute, 10)
	if len(got) != 1 {
		t.Fatal("not claimed")
	}
	e := got[0]
	if err := q.Retry(ctx, e.ID, e.Generation, t0.Add(time.Hour), true, "waiting for a scan"); err != nil {
		t.Fatal(err)
	}
	all, _ := q.List(ctx)
	if len(all) != 1 || all[0].Attempts != 1 || !all[0].WaitingForScan || all[0].Reason != "waiting for a scan" {
		t.Fatalf("after Retry: %+v", all)
	}
	if again, _ := q.Claim(ctx, t0.Add(time.Minute), time.Minute, 10); len(again) != 0 {
		t.Fatal("a rescheduled row was claimed early")
	}
	if err := q.WakeForScan(ctx, comp, t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if again, _ := q.Claim(ctx, t0.Add(time.Minute), time.Minute, 10); len(again) != 1 {
		t.Fatal("WakeForScan did not make the row due")
	}

	// A Retry for a generation a publish has replaced keeps the publish's due time.
	if _, err := q.EnqueuePublish(ctx, p.FromRepo, comp, t0.Add(2*time.Minute), t0.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := q.Retry(ctx, e.ID, e.Generation, t0.Add(10*time.Hour), true, "stale"); err != nil {
		t.Fatal(err)
	}
	all, _ = q.List(ctx)
	if len(all) != 1 || !all[0].DueAt.Equal(t0.Add(3*time.Minute)) || all[0].WaitingForScan || all[0].Attempts != 0 {
		t.Fatalf("stale Retry overrode the publish: %+v", all)
	}
	// A component deleted takes its rows with it.
	if _, err := pgtest.Pool(t).Exec(ctx, `DELETE FROM components WHERE id = $1`, comp); err != nil {
		t.Fatal(err)
	}
	if all, _ := q.List(ctx); len(all) != 0 {
		t.Fatalf("rows of a deleted component survive: %+v", all)
	}
}

// Replicas claiming at the same instant never share a row (SKIP LOCKED).
func TestAutoPromotionQueue_ConcurrentClaimsAreDisjoint(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Pool(t)
	q := NewAutoPromotionQueueRepo(pool)
	p, _, _ := autoQueueFixture(t, ctx, "aq3")
	t0 := time.Now().UTC()
	for i := 0; i < 40; i++ {
		c := makePromotionComponent(t, ctx, p.FromRepoID, "c"+string(rune('a'+i%26))+string(rune('a'+i/26)))
		if _, err := q.EnqueuePublish(ctx, p.FromRepo, c, t0, t0); err != nil {
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
				got, err := q.Claim(ctx, t0.Add(time.Second), time.Hour, 3)
				if err != nil || len(got) == 0 {
					return
				}
				mu.Lock()
				for _, e := range got {
					seen[e.ID]++
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

	first := &domain.PromotionRequest{RuleID: auto.ID, ComponentID: comp, Status: domain.PromotionPending}
	created, err := pr.CreateAutoRequest(ctx, first)
	if err != nil || !created {
		t.Fatalf("first = %v, %v", created, err)
	}
	second := &domain.PromotionRequest{RuleID: auto.ID, ComponentID: comp, Status: domain.PromotionPending}
	created, err = pr.CreateAutoRequest(ctx, second)
	if err != nil || created || second.ID != first.ID || !second.Automatic {
		t.Fatalf("second = %+v, %v, %v", second, created, err)
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
