package loginguard

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeClock lets the tests move time without sleeping.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time      { return c.t }
func (c *fakeClock) add(d time.Duration) { c.t = c.t.Add(d) }

func newTestGuard() (*Guard, *fakeClock) {
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	g := New(nil, Policy{Threshold: 3, BaseDelay: time.Second, MaxDelay: time.Minute})
	g.now = clk.now
	return g, clk
}

func TestGuard_UnderThreshold_NotBlocked(t *testing.T) {
	g, _ := newTestGuard()
	ctx := context.Background()

	// Threshold failures are tolerated, so a human fumbling a password is never
	// delayed. The penalty starts on the one after that.
	for range 3 {
		require.Zero(t, g.RetryAfter(ctx, "alice"))
		g.RecordFailure(ctx, "alice")
	}
	require.Zero(t, g.RetryAfter(ctx, "alice"), "three failures is still within the allowance")

	g.RecordFailure(ctx, "alice")
	assert.Positive(t, g.RetryAfter(ctx, "alice"))
}

func TestGuard_DelayGrowsAndIsCapped(t *testing.T) {
	g, clk := newTestGuard()
	ctx := context.Background()

	var delays []time.Duration
	for range 12 {
		g.RecordFailure(ctx, "alice")
		d := g.RetryAfter(ctx, "alice")
		delays = append(delays, d)
		clk.add(d) // wait it out, then fail again
	}

	assert.Zero(t, delays[0], "first failure must not block")
	assert.Zero(t, delays[2], "still inside the allowance")
	assert.Equal(t, time.Second, delays[3], "first penalty is BaseDelay")
	assert.Equal(t, 2*time.Second, delays[4])
	assert.Equal(t, 4*time.Second, delays[5])
	assert.Equal(t, time.Minute, delays[11], "capped at MaxDelay")
}

func TestGuard_WaitingItOutAllowsRetry(t *testing.T) {
	g, clk := newTestGuard()
	ctx := context.Background()

	for range 4 {
		g.RecordFailure(ctx, "alice")
	}
	blocked := g.RetryAfter(ctx, "alice")
	require.Positive(t, blocked)

	clk.add(blocked)
	assert.Zero(t, g.RetryAfter(ctx, "alice"), "the window has passed")
}

func TestGuard_SuccessResets(t *testing.T) {
	g, _ := newTestGuard()
	ctx := context.Background()

	for range 5 {
		g.RecordFailure(ctx, "alice")
	}
	require.Positive(t, g.RetryAfter(ctx, "alice"))

	g.RecordSuccess(ctx, "alice")
	assert.Zero(t, g.RetryAfter(ctx, "alice"))
}

func TestGuard_AccountsAreIndependent(t *testing.T) {
	g, _ := newTestGuard()
	ctx := context.Background()

	for range 5 {
		g.RecordFailure(ctx, "alice")
	}
	require.Positive(t, g.RetryAfter(ctx, "alice"))
	assert.Zero(t, g.RetryAfter(ctx, "bob"), "one account under attack must not lock out another")
}

// Usernames are counted case-insensitively, otherwise "Alice" and "alice" are
// two budgets for one account.
func TestGuard_UsernameCaseInsensitive(t *testing.T) {
	g, _ := newTestGuard()
	ctx := context.Background()

	for range 5 {
		g.RecordFailure(ctx, "Alice")
	}
	assert.Positive(t, g.RetryAfter(ctx, "alice"))
}

// A guard with no policy configured must never block — this is the path taken
// when the feature is switched off.
func TestGuard_Disabled_NeverBlocks(t *testing.T) {
	var g *Guard
	ctx := context.Background()

	for range 100 {
		g.RecordFailure(ctx, "alice")
	}
	assert.Zero(t, g.RetryAfter(ctx, "alice"))
	g.RecordSuccess(ctx, "alice")
}

func TestGuard_StaleEntriesAreEvicted(t *testing.T) {
	g, clk := newTestGuard()
	ctx := context.Background()

	g.RecordFailure(ctx, "alice")
	require.Equal(t, 1, g.size())

	clk.add(2 * entryTTL)
	g.RecordFailure(ctx, "bob") // any write triggers the sweep
	assert.Equal(t, 1, g.size(), "alice's stale entry should be gone")
}
