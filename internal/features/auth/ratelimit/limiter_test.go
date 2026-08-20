package ratelimit

import (
	"context"
	"strings"
	"testing"
	"time"
)

type captureStore struct {
	key   string
	count int64
	retry time.Duration
}

func (s *captureStore) Increment(
	_ context.Context,
	key string,
	_ time.Duration,
) (int64, time.Duration, error) {
	s.key = key
	return s.count, s.retry, nil
}

func (*captureStore) Close() error { return nil }

func TestLimiterUsesOpaqueSubjectKeyAndRejectsExceededRule(t *testing.T) {
	store := &captureStore{count: 6, retry: 30 * time.Second}
	limiter := newLimiter(store, []byte("01234567890123456789012345678901"), "test:auth")

	decision, err := limiter.Allow(
		context.Background(),
		"login:account",
		"family@example.com",
		Rule{Limit: 5, Window: time.Minute},
	)
	if err != nil {
		t.Fatalf("Allow() error = %v", err)
	}
	if decision.Allowed || decision.RetryAfter != 30*time.Second {
		t.Fatalf("decision = %#v", decision)
	}
	if strings.Contains(store.key, "family@example.com") ||
		!strings.HasPrefix(store.key, "test:auth:login:account:") {
		t.Fatalf("stored key = %q", store.key)
	}
}

func TestMemoryStoreResetsCounterAfterWindow(t *testing.T) {
	now := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	store := newMemoryStore(10)
	store.now = func() time.Time { return now }

	count, _, err := store.Increment(context.Background(), "key", time.Minute)
	if err != nil || count != 1 {
		t.Fatalf("first Increment() = %d, %v", count, err)
	}
	count, _, err = store.Increment(context.Background(), "key", time.Minute)
	if err != nil || count != 2 {
		t.Fatalf("second Increment() = %d, %v", count, err)
	}
	now = now.Add(time.Minute)
	count, retryAfter, err := store.Increment(context.Background(), "key", time.Minute)
	if err != nil || count != 1 || retryAfter != time.Minute {
		t.Fatalf("reset Increment() = count %d retry %s error %v", count, retryAfter, err)
	}
}

func TestMemoryStoreBoundsActiveKeys(t *testing.T) {
	store := newMemoryStore(1)
	if _, _, err := store.Increment(context.Background(), "first", time.Hour); err != nil {
		t.Fatalf("first Increment() error = %v", err)
	}
	if _, _, err := store.Increment(context.Background(), "second", time.Hour); err != errMemoryCapacity {
		t.Fatalf("second Increment() error = %v, want capacity", err)
	}
}
