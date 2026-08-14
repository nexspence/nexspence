package distlock_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nexspence-oss/nexspence/internal/distlock"
)

type stubRedis struct {
	keys      map[string]string
	setNXf    func(key string) (bool, error)
	delf      func(key string) error
	delMatchf func(key, value string) (bool, error)
}

func newStubRedis() *stubRedis {
	return &stubRedis{keys: make(map[string]string)}
}

func (s *stubRedis) SetNX(_ context.Context, key, value string, _ time.Duration) (bool, error) {
	if s.setNXf != nil {
		return s.setNXf(key)
	}
	if _, exists := s.keys[key]; exists {
		return false, nil
	}
	s.keys[key] = value
	return true, nil
}

func (s *stubRedis) Del(_ context.Context, key string) error {
	if s.delf != nil {
		return s.delf(key)
	}
	delete(s.keys, key)
	return nil
}

// DelIfMatch models the Redis-side compare-and-delete: the key is removed only
// while it still holds the exact value the caller wrote.
func (s *stubRedis) DelIfMatch(_ context.Context, key, value string) (bool, error) {
	if s.delMatchf != nil {
		return s.delMatchf(key, value)
	}
	if cur, exists := s.keys[key]; !exists || cur != value {
		return false, nil
	}
	delete(s.keys, key)
	return true, nil
}

func TestRedisLocker_AcquireRelease(t *testing.T) {
	stub := newStubRedis()
	l := distlock.NewRedisLocker(stub)

	lock, err := l.Acquire(context.Background(), "nexspence:lock:test", time.Minute)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if _, exists := stub.keys["nexspence:lock:test"]; !exists {
		t.Fatal("key not set after Acquire")
	}
	if err := lock.Release(context.Background()); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, exists := stub.keys["nexspence:lock:test"]; exists {
		t.Fatal("key still present after Release")
	}
}

func TestRedisLocker_AlreadyLocked(t *testing.T) {
	stub := newStubRedis()
	l := distlock.NewRedisLocker(stub)
	stub.keys["nexspence:lock:busy"] = "other-node"

	_, err := l.Acquire(context.Background(), "nexspence:lock:busy", time.Minute)
	if !errors.Is(err, distlock.ErrLockHeld) {
		t.Fatalf("want ErrLockHeld, got %v", err)
	}
}

// A holder whose TTL expired must not delete the lock a second node has since
// taken: Release is a compare-and-delete against the token Acquire wrote.
func TestRedisLocker_ReleaseAfterExpiryKeepsNewHoldersLock(t *testing.T) {
	const key = "nexspence:lock:expired"
	stub := newStubRedis()
	l := distlock.NewRedisLocker(stub)

	stale, err := l.Acquire(context.Background(), key, time.Minute)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}

	// The TTL runs out while the first holder is still working; a second node
	// legitimately takes the lock for its own run.
	delete(stub.keys, key)
	if _, err := l.Acquire(context.Background(), key, time.Minute); err != nil {
		t.Fatalf("second Acquire: %v", err)
	}
	newToken := stub.keys[key]

	if err := stale.Release(context.Background()); err != nil {
		t.Fatalf("stale Release: %v", err)
	}

	if got := stub.keys[key]; got != newToken {
		t.Fatalf("stale Release clobbered the new holder's lock: key = %q, want %q", got, newToken)
	}
}

func TestRedisLocker_ReleaseError(t *testing.T) {
	stub := newStubRedis()
	stub.delMatchf = func(string, string) (bool, error) { return false, errors.New("redis down") }
	l := distlock.NewRedisLocker(stub)

	lock, err := l.Acquire(context.Background(), "nexspence:lock:x", time.Minute)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := lock.Release(context.Background()); err == nil {
		t.Fatal("want error from Release, got nil")
	}
}

// ForceRelease exists for cleaning up after a holder that can no longer release
// its own lock, so it drops the key whoever wrote it.
func TestRedisLocker_ForceReleaseDropsAnotherHoldersKey(t *testing.T) {
	const key = "nexspence:lock:crashed"
	stub := newStubRedis()
	stub.keys[key] = "token-from-a-dead-node"
	l := distlock.NewRedisLocker(stub)

	if err := l.ForceRelease(context.Background(), key); err != nil {
		t.Fatalf("ForceRelease: %v", err)
	}
	if _, exists := stub.keys[key]; exists {
		t.Fatal("key still held after ForceRelease")
	}

	// The lock is free for the next node to take.
	if _, err := l.Acquire(context.Background(), key, time.Minute); err != nil {
		t.Fatalf("Acquire after ForceRelease: %v", err)
	}
}

func TestRedisLocker_ForceReleaseError(t *testing.T) {
	stub := newStubRedis()
	stub.delf = func(string) error { return errors.New("redis down") }
	l := distlock.NewRedisLocker(stub)

	if err := l.ForceRelease(context.Background(), "nexspence:lock:x"); err == nil {
		t.Fatal("want error from ForceRelease, got nil")
	}
}
