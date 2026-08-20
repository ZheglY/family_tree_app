package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

var incrementScript = redis.NewScript(`
local count = redis.call("INCR", KEYS[1])
if count == 1 then
  redis.call("PEXPIRE", KEYS[1], ARGV[1])
end
local ttl = redis.call("PTTL", KEYS[1])
return {count, ttl}
`)

type redisStore struct {
	client  *redis.Client
	timeout time.Duration
}

func newRedisStore(ctx context.Context, config Config) (*redisStore, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     config.RedisAddress,
		Username: config.RedisUsername,
		Password: config.RedisPassword,
		DB:       config.RedisDB,
	})
	store := &redisStore{client: client, timeout: config.RedisTimeout}
	checkContext, cancel := context.WithTimeout(ctx, config.RedisTimeout)
	defer cancel()
	if err := client.Ping(checkContext).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("ping rate limit Redis: %w", err)
	}
	return store, nil
}

func (s *redisStore) Increment(
	ctx context.Context,
	key string,
	window time.Duration,
) (int64, time.Duration, error) {
	callContext, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	values, err := incrementScript.Run(
		callContext,
		s.client,
		[]string{key},
		window.Milliseconds(),
	).Int64Slice()
	if err != nil {
		return 0, 0, fmt.Errorf("execute Redis rate limit script: %w", err)
	}
	if len(values) != 2 {
		return 0, 0, fmt.Errorf("unexpected Redis rate limit response")
	}
	retryAfter := time.Duration(values[1]) * time.Millisecond
	if retryAfter <= 0 {
		retryAfter = window
	}
	return values[0], retryAfter, nil
}

func (s *redisStore) Close() error {
	if s == nil || s.client == nil {
		return nil
	}
	return s.client.Close()
}
