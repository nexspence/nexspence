package loginguard

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// kv is the slice of redisclient.Client this store needs.
type kv interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string, ttl time.Duration) error
	Del(ctx context.Context, key string) error
}

// redisStore shares failure counts across HA nodes so that spreading guesses
// over several servers does not multiply the allowance.
//
// Every operation is best-effort: Redis being unreachable degrades the guard to
// per-node counting rather than locking anyone out or failing the login path.
type redisStore struct{ c kv }

// NewRedisStore adapts a Redis client to Store.
func NewRedisStore(c kv) Store { return &redisStore{c: c} }

func redisKey(account string) string { return "nexspence:loginfail:" + account }

func (s *redisStore) Load(ctx context.Context, key string) (int, time.Time, bool) {
	raw, err := s.c.Get(ctx, redisKey(key))
	if err != nil || raw == "" {
		return 0, time.Time{}, false
	}
	failures, blockedUntil, ok := parseEntry(raw)
	if !ok {
		return 0, time.Time{}, false
	}
	return failures, blockedUntil, true
}

func (s *redisStore) Save(ctx context.Context, key string, failures int, blockedUntil time.Time, ttl time.Duration) {
	_ = s.c.Set(ctx, redisKey(key), formatEntry(failures, blockedUntil), ttl)
}

func (s *redisStore) Delete(ctx context.Context, key string) {
	_ = s.c.Del(ctx, redisKey(key))
}

// formatEntry encodes as "<failures>:<unixNano>" — small, and readable when
// someone is staring at redis-cli during an incident.
func formatEntry(failures int, blockedUntil time.Time) string {
	return fmt.Sprintf("%d:%d", failures, blockedUntil.UnixNano())
}

func parseEntry(raw string) (int, time.Time, bool) {
	parts := strings.SplitN(raw, ":", 2)
	if len(parts) != 2 {
		return 0, time.Time{}, false
	}
	failures, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, time.Time{}, false
	}
	nanos, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return 0, time.Time{}, false
	}
	return failures, time.Unix(0, nanos), true
}
