package distlock

import (
	"context"
	"errors"
	"time"
)

var ErrLockHeld = errors.New("distlock: lock already held")

type Locker interface {
	Acquire(ctx context.Context, key string, ttl time.Duration) (Lock, error)
}

type Lock interface {
	Release(ctx context.Context) error
}
