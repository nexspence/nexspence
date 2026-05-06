package distlock

import (
	"context"
	"time"
)

type NoopLocker struct{}

func (NoopLocker) Acquire(_ context.Context, _ string, _ time.Duration) (Lock, error) {
	return noopLock{}, nil
}

type noopLock struct{}

func (noopLock) Release(_ context.Context) error { return nil }
