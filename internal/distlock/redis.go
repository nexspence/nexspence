package distlock

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type RedisBackend interface {
	SetNX(ctx context.Context, key, value string, ttl time.Duration) (bool, error)
	Del(ctx context.Context, key string) error
}

type RedisLocker struct {
	rdb RedisBackend
}

func NewRedisLocker(rdb RedisBackend) *RedisLocker {
	return &RedisLocker{rdb: rdb}
}

func (l *RedisLocker) Acquire(ctx context.Context, key string, ttl time.Duration) (Lock, error) {
	token := uuid.New().String()
	ok, err := l.rdb.SetNX(ctx, key, token, ttl)
	if err != nil {
		return nil, fmt.Errorf("distlock acquire %q: %w", key, err)
	}
	if !ok {
		return nil, ErrLockHeld
	}
	return &redisLock{rdb: l.rdb, key: key}, nil
}

type redisLock struct {
	rdb RedisBackend
	key string
}

func (l *redisLock) Release(ctx context.Context) error {
	if err := l.rdb.Del(ctx, l.key); err != nil {
		return fmt.Errorf("distlock release %q: %w", l.key, err)
	}
	return nil
}
