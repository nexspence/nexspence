package distlock

import (
	"context"
	"time"
)

// NoopLocker is a Locker that always succeeds without coordination,
// used when distributed locking is disabled (single-node mode).
type NoopLocker struct{}

// Acquire always returns a no-op lock that does nothing on release.
func (NoopLocker) Acquire(_ context.Context, _ string, _ time.Duration) (Lock, error) {
	return noopLock{}, nil
}

type noopLock struct{}

func (noopLock) Release(_ context.Context) error { return nil }
