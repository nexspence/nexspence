package distlock_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nexspence-oss/nexspence/internal/distlock"
)

type stubRedis struct {
	keys   map[string]string
	setNXf func(key string) (bool, error)
	delf   func(key string) error
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

func TestRedisLocker_ReleaseError(t *testing.T) {
	stub := newStubRedis()
	stub.delf = func(key string) error { return errors.New("redis down") }
	l := distlock.NewRedisLocker(stub)

	lock, err := l.Acquire(context.Background(), "nexspence:lock:x", time.Minute)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := lock.Release(context.Background()); err == nil {
		t.Fatal("want error from Release, got nil")
	}
}
