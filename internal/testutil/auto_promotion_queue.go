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
// publish refreshes the row and bumps its generation, claims are leases.
// Rules are read from the PromotionRepo it is built over, the way the SQL
// joins promotion_rules.
type AutoPromotionQueue struct {
	mu      sync.Mutex
	rules   *PromotionRepo
	entries map[string]*autoQueueRow
	nextID  int

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

func (q *AutoPromotionQueue) find(ruleID, componentID string) *autoQueueRow {
	for _, e := range q.entries {
		if e.RuleID == ruleID && e.ComponentID == componentID {
			return e
		}
	}
	return nil
}

// EnqueuePublish implements repository.AutoPromotionQueueRepo.
func (q *AutoPromotionQueue) EnqueuePublish(ctx context.Context, fromRepo, componentID string, publishedAt, dueAt time.Time) (int, error) {
	if q.Err != nil {
		return 0, q.Err
	}
	rules, _ := q.rules.ListRulesByFromRepo(ctx, fromRepo)
	q.mu.Lock()
	defer q.mu.Unlock()
	n := 0
	for _, r := range rules {
		if !r.AutoPromote {
			continue
		}
		n++
		if e := q.find(r.ID, componentID); e != nil {
			e.LastPublishedAt = publishedAt
			e.DueAt = dueAt
			e.Generation++
			e.Attempts = 0
			e.WaitingForScan = false
			e.Reason = ""
			continue
		}
		q.nextID++
		id := fmt.Sprintf("autoq-%d", q.nextID)
		q.entries[id] = &autoQueueRow{AutoPromotionEntry: domain.AutoPromotionEntry{
			ID: id, RuleID: r.ID, ComponentID: componentID,
			LastPublishedAt: publishedAt, DueAt: dueAt, Generation: 1,
		}}
	}
	return n, nil
}

// Claim implements repository.AutoPromotionQueueRepo.
func (q *AutoPromotionQueue) Claim(_ context.Context, now time.Time, lease time.Duration, limit int) ([]domain.AutoPromotionEntry, error) {
	if q.Err != nil {
		return nil, q.Err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
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
		e.claimedUntil = now.Add(lease)
		out = append(out, e.AutoPromotionEntry)
	}
	return out, nil
}

// Finish implements repository.AutoPromotionQueueRepo.
func (q *AutoPromotionQueue) Finish(_ context.Context, id string, generation int64) error {
	if q.Err != nil {
		return q.Err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	e, ok := q.entries[id]
	if !ok {
		return nil
	}
	if e.Generation == generation {
		delete(q.entries, id)
		return nil
	}
	e.claimedUntil = time.Time{}
	return nil
}

// Retry implements repository.AutoPromotionQueueRepo.
func (q *AutoPromotionQueue) Retry(_ context.Context, id string, generation int64, dueAt time.Time, waitingForScan bool, reason string) error {
	if q.Err != nil {
		return q.Err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	e, ok := q.entries[id]
	if !ok {
		return nil
	}
	e.claimedUntil = time.Time{}
	if e.Generation == generation {
		e.DueAt = dueAt
		e.Attempts++
		e.WaitingForScan = waitingForScan
		e.Reason = reason
	}
	return nil
}

// WakeForScan implements repository.AutoPromotionQueueRepo.
func (q *AutoPromotionQueue) WakeForScan(_ context.Context, componentID string, now time.Time) error {
	if q.Err != nil {
		return q.Err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, e := range q.entries {
		if e.ComponentID == componentID && e.WaitingForScan && e.DueAt.After(now) {
			e.DueAt = now
		}
	}
	return nil
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
