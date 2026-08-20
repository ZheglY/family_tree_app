package ratelimit

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRedisLimiterSharesCounterIntegration(t *testing.T) {
	address := os.Getenv("AUTH_RATE_LIMIT_TEST_REDIS_ADDR")
	if address == "" {
		t.Skip("AUTH_RATE_LIMIT_TEST_REDIS_ADDR is not set")
	}
	config := Config{
		Enabled:       true,
		Backend:       "redis",
		KeyPrefix:     "family-tree:test:" + uuid.NewString(),
		KeySecret:     "01234567890123456789012345678901",
		RedisAddress:  address,
		RedisTimeout:  time.Second,
		MemoryMaxKeys: 10,
	}
	first, err := New(context.Background(), config)
	if err != nil {
		t.Fatalf("New(first) error = %v", err)
	}
	t.Cleanup(func() { _ = first.Close() })
	second, err := New(context.Background(), config)
	if err != nil {
		t.Fatalf("New(second) error = %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })

	rule := Rule{Limit: 1, Window: time.Minute}
	decision, err := first.Allow(context.Background(), "login:ip", "127.0.0.1", rule)
	if err != nil || !decision.Allowed {
		t.Fatalf("first Allow() = %#v, %v", decision, err)
	}
	decision, err = second.Allow(context.Background(), "login:ip", "127.0.0.1", rule)
	if err != nil {
		t.Fatalf("second Allow() error = %v", err)
	}
	if decision.Allowed || decision.RetryAfter <= 0 {
		t.Fatalf("second decision = %#v", decision)
	}
}
