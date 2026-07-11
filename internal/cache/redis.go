package cache

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// Cache is the minimal contract the HTTP cache middleware depends on. Keeping it
// as an interface lets the middleware be a no-op when caching is disabled and
// makes the service easy to test or swap to a different backend later.
type Cache interface {
	Get(ctx context.Context, key string) ([]byte, bool, error)
	Set(ctx context.Context, key string, val []byte, ttl time.Duration) error
	DeleteByPrefix(ctx context.Context, prefix string) error
	Close() error
}

// RedisCache is a Redis-backed Cache implementation.
type RedisCache struct {
	client *redis.Client
}

// NewRedisCache builds a Redis client from a connection URL such as
// "redis://localhost:6379/0" or "rediss://user:pass@host:6379/0".
// Timeouts are kept short so a slow or unreachable Redis never stalls requests.
func NewRedisCache(url string) (*RedisCache, error) {
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, err
	}

	opts.DialTimeout = 3 * time.Second
	opts.ReadTimeout = 500 * time.Millisecond
	opts.WriteTimeout = 500 * time.Millisecond
	opts.PoolSize = 20

	return &RedisCache{client: redis.NewClient(opts)}, nil
}

// Ping verifies connectivity to Redis.
func (c *RedisCache) Ping(ctx context.Context) error {
	return c.client.Ping(ctx).Err()
}

func (c *RedisCache) Get(ctx context.Context, key string) ([]byte, bool, error) {
	b, err := c.client.Get(ctx, key).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return b, true, nil
}

func (c *RedisCache) Set(ctx context.Context, key string, val []byte, ttl time.Duration) error {
	return c.client.Set(ctx, key, val, ttl).Err()
}

// DeleteByPrefix removes every key matching prefix*. Intended to be called by an
// ingestion job after new readings land so fresh data is served immediately
// instead of waiting for TTL expiry.
func (c *RedisCache) DeleteByPrefix(ctx context.Context, prefix string) error {
	var cursor uint64
	for {
		keys, next, err := c.client.Scan(ctx, cursor, prefix+"*", 200).Result()
		if err != nil {
			return err
		}
		if len(keys) > 0 {
			if err := c.client.Del(ctx, keys...).Err(); err != nil {
				return err
			}
		}
		cursor = next
		if cursor == 0 {
			return nil
		}
	}
}

func (c *RedisCache) Close() error {
	return c.client.Close()
}
