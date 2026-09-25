package testutil

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/nexspence-oss/nexspence/internal/domain"
	"github.com/nexspence-oss/nexspence/internal/repository"
)

var _ repository.AutoPromotionQueueRepo = (*AutoPromotionQueue)(nil)

// AutoPromotionQueue is an in-memory repository.AutoPromotionQueueRepo with the
// postgres implementation's semantics: one row per (rule, component), a
// publish refreshes the row and bumps its generation, claims are leases under
// a token, and last_published_at only moves forward. Rules are read from the
// PromotionRepo it is built over, the way the SQL joins promotion_rules. Now
// stands in for the database clock.
type AutoPromotionQueue struct {
	mu      sync.Mutex
	rules   *PromotionRepo
	entries map[string]*autoQueueRow
	nextID  int

	// Now is the "database" clock; nil means time.Now.
	Now func() time.Time
	// Err, when set, is returned by every method.
	Err error
}

type autoQueueRow struct {
	domain.AutoPromotionEntry
	claimedUntil time.Time
}

// NewAutoPromotionQueue returns an empty queue over rules.
func NewAutoPromotionQueue(rules *PromotionRepo) *AutoPromotionQueue {
	return &AutoPromotionQueue{rules: rules, entries: map[string]*autoQueueRow{}}
}

func (q *AutoPromotionQueue) now() time.Time {
	if q.Now != nil {
		return q.Now()
	}
	return time.Now()
}

func (q *AutoPromotionQueue) find(ruleID, componentID string) *autoQueueRow {
	for _, e := range q.entries {
		if e.RuleID == ruleID && e.ComponentID == componentID {
			return e
		}
	}
	return nil
}

// EnqueuePublish implements repository.AutoPromotionQueueRepo.
func (q *AutoPromotionQueue) EnqueuePublish(ctx context.Context, fromRepo, componentID string, publishedAt time.Time, settle time.Duration) (int, error) {
	if q.Err != nil {
		return 0, q.Err
	}
	rules, _ := q.rules.ListRulesByFromRepo(ctx, fromRepo)
	q.mu.Lock()
	defer q.mu.Unlock()
	due := q.now().Add(settle)
	n := 0
	for _, r := range rules {
		if !r.AutoPromote {
			continue
		}
		n++
		if e := q.find(r.ID, componentID); e != nil {
			if publishedAt.After(e.LastPublishedAt) {
				e.LastPublishedAt = publishedAt
			}
			e.DueAt = due
			e.Generation++
			e.Attempts = 0
			e.WaitingForScan = false
			e.Started = false
			e.Reason = ""
			continue
		}
		q.nextID++
		id := fmt.Sprintf("autoq-%d", q.nextID)
		q.entries[id] = &autoQueueRow{AutoPromotionEntry: domain.AutoPromotionEntry{
			ID: id, RuleID: r.ID, ComponentID: componentID,
			LastPublishedAt: publishedAt, DueAt: due, Generation: 1,
		}}
	}
	return n, nil
}

// Claim implements repository.AutoPromotionQueueRepo.
func (q *AutoPromotionQueue) Claim(_ context.Context, lease time.Duration, limit int) ([]domain.AutoPromotionEntry, error) {
	if q.Err != nil {
		return nil, q.Err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	now := q.now()
	var due []*autoQueueRow
	for _, e := range q.entries {
		if !e.DueAt.After(now) && !e.claimedUntil.After(now) {
			due = append(due, e)
		}
	}
	sort.Slice(due, func(i, j int) bool { return due[i].DueAt.Before(due[j].DueAt) })
	if len(due) > limit {
		due = due[:limit]
	}
	out := make([]domain.AutoPromotionEntry, 0, len(due))
	for _, e := range due {
		q.nextID++
		e.claimedUntil = now.Add(lease)
		e.ClaimToken = fmt.Sprintf("claim-%d", q.nextID)
		out = append(out, e.AutoPromotionEntry)
	}
	return out, nil
}

// owned returns the row e names if e still holds its claim.
func (q *AutoPromotionQueue) owned(e domain.AutoPromotionEntry) *autoQueueRow {
	row, ok := q.entries[e.ID]
	if !ok || row.ClaimToken == "" || row.ClaimToken != e.ClaimToken {
		return nil
	}
	return row
}

// Finish implements repository.AutoPromotionQueueRepo.
func (q *AutoPromotionQueue) Finish(_ context.Context, e domain.AutoPromotionEntry) error {
	if q.Err != nil {
		return q.Err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	row := q.owned(e)
	if row == nil {
		return nil
	}
	if row.Generation == e.Generation {
		delete(q.entries, e.ID)
		return nil
	}
	row.claimedUntil, row.ClaimToken = time.Time{}, ""
	return nil
}

// Retry implements repository.AutoPromotionQueueRepo.
func (q *AutoPromotionQueue) Retry(_ context.Context, e domain.AutoPromotionEntry, r repository.AutoPromotionRetry) error {
	if q.Err != nil {
		return q.Err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	row := q.owned(e)
	if row == nil {
		return nil
	}
	row.claimedUntil, row.ClaimToken = time.Time{}, ""
	if row.Generation == e.Generation {
		row.DueAt = q.now().Add(r.DueIn)
		if r.CountAttempt {
			row.Attempts++
		}
		row.WaitingForScan = r.WaitingForScan
		row.Started = r.Started
		row.Reason = r.Reason
	}
	return nil
}

// WakeForScan implements repository.AutoPromotionQueueRepo.
func (q *AutoPromotionQueue) WakeForScan(_ context.Context, componentID string) error {
	if q.Err != nil {
		return q.Err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	now := q.now()
	for _, e := range q.entries {
		if e.ComponentID == componentID && e.WaitingForScan && e.DueAt.After(now) {
			e.DueAt = now
		}
	}
	return nil
}

// Queued implements repository.AutoPromotionQueueRepo.
func (q *AutoPromotionQueue) Queued(_ context.Context, ruleID, componentID string) (bool, error) {
	if q.Err != nil {
		return false, q.Err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.find(ruleID, componentID) != nil, nil
}

// List implements repository.AutoPromotionQueueRepo.
func (q *AutoPromotionQueue) List(_ context.Context) ([]domain.AutoPromotionEntry, error) {
	if q.Err != nil {
		return nil, q.Err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]domain.AutoPromotionEntry, 0, len(q.entries))
	for _, e := range q.entries {
		out = append(out, e.AutoPromotionEntry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DueAt.Before(out[j].DueAt) })
	return out, nil
}
