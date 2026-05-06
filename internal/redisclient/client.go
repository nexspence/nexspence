package redisclient

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
	nexspencecfg "github.com/nexspence-oss/nexspence/internal/config"
)

// Client wraps go-redis with the subset of operations Nexspence needs.
type Client struct {
	rdb *redis.Client
}

// New connects to Redis using cfg. Returns an error if the connection cannot be
// established. Callers should check cfg.Enabled before calling New.
func New(cfg nexspencecfg.RedisConfig) (*Client, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		return nil, err
	}
	return &Client{rdb: rdb}, nil
}

func (c *Client) Get(ctx context.Context, key string) (string, error) {
	return c.rdb.Get(ctx, key).Result()
}

func (c *Client) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	return c.rdb.Set(ctx, key, value, ttl).Err()
}

// SetNX sets key=value with TTL only if the key does not exist.
// Returns true if the key was set, false if it already existed.
func (c *Client) SetNX(ctx context.Context, key, value string, ttl time.Duration) (bool, error) {
	return c.rdb.SetNX(ctx, key, value, ttl).Result()
}

func (c *Client) Del(ctx context.Context, key string) error {
	return c.rdb.Del(ctx, key).Err()
}

func (c *Client) Ping(ctx context.Context) error {
	return c.rdb.Ping(ctx).Err()
}

func (c *Client) Close() error {
	return c.rdb.Close()
}
