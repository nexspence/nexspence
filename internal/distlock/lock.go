package distlock

import (
	"context"
	"errors"
	"time"
)

// ErrLockHeld is returned by Acquire when the lock is already held by another caller.
var ErrLockHeld = errors.New("distlock: lock already held")

// Locker acquires distributed locks identified by key.
type Locker interface {
	Acquire(ctx context.Context, key string, ttl time.Duration) (Lock, error)
	// ForceRelease drops key whoever holds it. It exists for cleaning up after a
	// holder that can no longer release its own lock — a node that crashed
	// mid-run — where waiting out the TTL would block the work for hours. Every
	// other caller releases through the Lock it acquired.
	ForceRelease(ctx context.Context, key string) error
}

// Lock represents an acquired distributed lock that can be released.
type Lock interface {
	Release(ctx context.Context) error
}
