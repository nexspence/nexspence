package distlock_test

import (
	"context"
	"testing"
	"time"

	"github.com/nexspence-oss/nexspence/internal/distlock"
)

func TestNoopLocker_AcquireRelease(t *testing.T) {
	var l distlock.Locker = distlock.NoopLocker{}
	lock, err := l.Acquire(context.Background(), "any-key", time.Minute)
	if err != nil {
		t.Fatalf("Acquire: unexpected error: %v", err)
	}
	if err := lock.Release(context.Background()); err != nil {
		t.Fatalf("Release: unexpected error: %v", err)
	}
}

func TestNoopLocker_MultipleAcquireSameKey(t *testing.T) {
	var l distlock.Locker = distlock.NoopLocker{}
	lock1, err := l.Acquire(context.Background(), "same-key", time.Minute)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	lock2, err := l.Acquire(context.Background(), "same-key", time.Minute)
	if err != nil {
		t.Fatalf("second Acquire: %v", err)
	}
	_ = lock1.Release(context.Background())
	_ = lock2.Release(context.Background())
}
