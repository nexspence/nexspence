package loginguard

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeKV stands in for redisclient.Client.
type fakeKV struct {
	data   map[string]string
	ttls   map[string]time.Duration
	getErr error
	setErr error
}

func newFakeKV() *fakeKV {
	return &fakeKV{data: map[string]string{}, ttls: map[string]time.Duration{}}
}

func (f *fakeKV) Get(_ context.Context, key string) (string, error) {
	if f.getErr != nil {
		return "", f.getErr
	}
	v, ok := f.data[key]
	if !ok {
		return "", errors.New("redis: nil")
	}
	return v, nil
}

func (f *fakeKV) Set(_ context.Context, key, value string, ttl time.Duration) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.data[key] = value
	f.ttls[key] = ttl
	return nil
}

func (f *fakeKV) Del(_ context.Context, key string) error {
	delete(f.data, key)
	return nil
}

func TestRedisStore_RoundTrip(t *testing.T) {
	kv := newFakeKV()
	s := NewRedisStore(kv)
	ctx := context.Background()
	until := time.Unix(1_700_000_123, 0)

	s.Save(ctx, "alice", 7, until, 30*time.Minute)

	failures, blockedUntil, ok := s.Load(ctx, "alice")
	require.True(t, ok)
	assert.Equal(t, 7, failures)
	assert.True(t, blockedUntil.Equal(until), "got %v want %v", blockedUntil, until)
	assert.Equal(t, 30*time.Minute, kv.ttls[redisKey("alice")])
}

func TestRedisStore_MissingKey(t *testing.T) {
	s := NewRedisStore(newFakeKV())
	_, _, ok := s.Load(context.Background(), "nobody")
	assert.False(t, ok)
}

// Redis being unreachable must not lock everyone out, nor blow up the login
// path — the guard falls back to its in-process count.
func TestRedisStore_ErrorIsNotFatal(t *testing.T) {
	kv := newFakeKV()
	kv.getErr = errors.New("connection refused")
	kv.setErr = errors.New("connection refused")
	s := NewRedisStore(kv)
	ctx := context.Background()

	s.Save(ctx, "alice", 3, time.Now(), time.Minute) // must not panic
	_, _, ok := s.Load(ctx, "alice")
	assert.False(t, ok)
}

func TestRedisStore_CorruptValueIgnored(t *testing.T) {
	kv := newFakeKV()
	kv.data[redisKey("alice")] = "garbage"
	s := NewRedisStore(kv)

	_, _, ok := s.Load(context.Background(), "alice")
	assert.False(t, ok, "an unparsable value must read as absent, not as zero failures")
}

func TestRedisStore_Delete(t *testing.T) {
	kv := newFakeKV()
	s := NewRedisStore(kv)
	ctx := context.Background()

	s.Save(ctx, "alice", 4, time.Now().Add(time.Minute), time.Minute)
	s.Delete(ctx, "alice")

	_, _, ok := s.Load(ctx, "alice")
	assert.False(t, ok)
}

// The shared store is what stops an attacker spreading guesses across HA nodes
// to multiply the allowance.
func TestGuard_SharedStoreCountsAcrossNodes(t *testing.T) {
	kv := newFakeKV()
	ctx := context.Background()
	policy := Policy{Threshold: 3, BaseDelay: time.Second, MaxDelay: time.Minute}

	nodeA := New(NewRedisStore(kv), policy)
	nodeB := New(NewRedisStore(kv), policy)

	for range 2 {
		nodeA.RecordFailure(ctx, "alice")
	}
	require.Zero(t, nodeB.RetryAfter(ctx, "alice"))

	nodeB.RecordFailure(ctx, "alice")
	nodeB.RecordFailure(ctx, "alice")

	assert.Positive(t, nodeA.RetryAfter(ctx, "alice"),
		"the fourth failure was on another node, but the budget is shared")
}
