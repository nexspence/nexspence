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
}

// Lock represents an acquired distributed lock that can be released.
type Lock interface {
	Release(ctx context.Context) error
}
