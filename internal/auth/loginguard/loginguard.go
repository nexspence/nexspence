// Package loginguard throttles password guessing per account.
//
// It deliberately keys on the username rather than the client address: the
// rate limiter already buckets by IP, and an IP is only as trustworthy as the
// proxy configuration behind it. An attacker rotating addresses still spends
// the same budget against one account.
package loginguard

import (
	"context"
	"strings"
	"sync"
	"time"
)

// entryTTL is how long a quiet account keeps its failure count. Long enough
// that a slow drip of guesses still accumulates, short enough that a user who
// mistyped a password this morning is not penalized tonight.
const entryTTL = 30 * time.Minute

// Policy describes how quickly failures escalate.
type Policy struct {
	// Threshold is how many failures are tolerated before delays begin, so a
	// human fumbling a password is never affected.
	Threshold int
	// BaseDelay is the first penalty; each further failure doubles it.
	BaseDelay time.Duration
	// MaxDelay caps the penalty.
	MaxDelay time.Duration
}

// DefaultPolicy is what the server uses unless configured otherwise: five free
// attempts, then 1s doubling up to 15 minutes.
func DefaultPolicy() Policy {
	return Policy{Threshold: 5, BaseDelay: time.Second, MaxDelay: 15 * time.Minute}
}

type state struct {
	failures     int
	blockedUntil time.Time
	lastSeen     time.Time
}

// Store persists failure state across nodes. Implementations must tolerate a
// missing key by returning ok=false.
type Store interface {
	Load(ctx context.Context, key string) (failures int, blockedUntil time.Time, ok bool)
	Save(ctx context.Context, key string, failures int, blockedUntil time.Time, ttl time.Duration)
	Delete(ctx context.Context, key string)
}

// Guard tracks consecutive login failures per account. A nil *Guard is a valid
// no-op guard, which is what the server uses when the feature is off.
type Guard struct {
	policy Policy
	store  Store // nil = single-node, in-process only

	mu      sync.Mutex
	entries map[string]*state

	now func() time.Time
}

// New builds a Guard. store may be nil for single-node deployments; when set
// (Redis), the count is shared so an attacker cannot spread attempts across
// nodes to multiply the allowance.
func New(store Store, policy Policy) *Guard {
	return &Guard{
		policy:  policy,
		store:   store,
		entries: make(map[string]*state),
		now:     time.Now,
	}
}

// RetryAfter reports how long the caller must wait before another attempt on
// this account is worth making. Zero means "go ahead".
func (g *Guard) RetryAfter(ctx context.Context, username string) time.Duration {
	if g == nil {
		return 0
	}
	key := normalize(username)
	now := g.now()

	_, blockedUntil := g.load(ctx, key)
	if blockedUntil.After(now) {
		return blockedUntil.Sub(now)
	}
	return 0
}

// RecordFailure counts one failed attempt and extends the penalty window.
func (g *Guard) RecordFailure(ctx context.Context, username string) {
	if g == nil {
		return
	}
	key := normalize(username)
	now := g.now()

	failures, _ := g.load(ctx, key)
	failures++
	blockedUntil := now.Add(g.penalty(failures))

	g.mu.Lock()
	g.sweep(now)
	g.entries[key] = &state{failures: failures, blockedUntil: blockedUntil, lastSeen: now}
	g.mu.Unlock()

	if g.store != nil {
		g.store.Save(ctx, key, failures, blockedUntil, entryTTL)
	}
}

// RecordSuccess clears the account's history — the credentials were right, so
// whatever came before was noise rather than an attack in progress.
func (g *Guard) RecordSuccess(ctx context.Context, username string) {
	if g == nil {
		return
	}
	key := normalize(username)

	g.mu.Lock()
	delete(g.entries, key)
	g.mu.Unlock()

	if g.store != nil {
		g.store.Delete(ctx, key)
	}
}

// penalty returns the wait imposed after the given number of failures.
func (g *Guard) penalty(failures int) time.Duration {
	over := failures - g.policy.Threshold
	if over <= 0 {
		return 0
	}
	d := g.policy.BaseDelay
	for range over - 1 {
		d *= 2
		if d >= g.policy.MaxDelay {
			return g.policy.MaxDelay
		}
	}
	if d > g.policy.MaxDelay {
		return g.policy.MaxDelay
	}
	return d
}

// load reads the shared store first so that HA nodes agree, falling back to the
// in-process map when there is no store or the key is absent from it.
func (g *Guard) load(ctx context.Context, key string) (int, time.Time) {
	if g.store != nil {
		if failures, blockedUntil, ok := g.store.Load(ctx, key); ok {
			return failures, blockedUntil
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if e, ok := g.entries[key]; ok {
		return e.failures, e.blockedUntil
	}
	return 0, time.Time{}
}

// sweep drops entries nobody has touched for entryTTL. Callers hold g.mu.
func (g *Guard) sweep(now time.Time) {
	cutoff := now.Add(-entryTTL)
	for k, e := range g.entries {
		if e.lastSeen.Before(cutoff) {
			delete(g.entries, k)
		}
	}
}

// size reports how many accounts are being tracked in process. Test hook.
func (g *Guard) size() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.entries)
}

func normalize(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}
