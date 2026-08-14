package distlock

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// RedisBackend is the subset of Redis operations RedisLocker depends on.
type RedisBackend interface {
	SetNX(ctx context.Context, key, value string, ttl time.Duration) (bool, error)
	Del(ctx context.Context, key string) error
	// DelIfMatch deletes key only while it still holds value, reporting whether
	// it did. It must be atomic on the Redis side.
	DelIfMatch(ctx context.Context, key, value string) (bool, error)
}

// RedisLocker implements Locker using Redis SetNX for mutual exclusion across nodes.
type RedisLocker struct {
	rdb RedisBackend
}

// NewRedisLocker creates a RedisLocker backed by the given Redis backend.
func NewRedisLocker(rdb RedisBackend) *RedisLocker {
	return &RedisLocker{rdb: rdb}
}

// Acquire takes the lock for key with the given TTL, returning ErrLockHeld if already held.
func (l *RedisLocker) Acquire(ctx context.Context, key string, ttl time.Duration) (Lock, error) {
	token := uuid.New().String()
	ok, err := l.rdb.SetNX(ctx, key, token, ttl)
	if err != nil {
		return nil, fmt.Errorf("distlock acquire %q: %w", key, err)
	}
	if !ok {
		return nil, ErrLockHeld
	}
	return &redisLock{rdb: l.rdb, key: key, token: token}, nil
}

type redisLock struct {
	rdb   RedisBackend
	key   string
	token string
}

// Release drops the lock only if this holder still owns it. A holder whose TTL
// expired mid-run must not delete the key another node has legitimately taken
// since — deleting it unconditionally would let a third node in while that
// second run is still going.
func (l *redisLock) Release(ctx context.Context) error {
	if _, err := l.rdb.DelIfMatch(ctx, l.key, l.token); err != nil {
		return fmt.Errorf("distlock release %q: %w", l.key, err)
	}
	return nil
}
