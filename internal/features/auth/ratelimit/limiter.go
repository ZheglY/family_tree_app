package ratelimit

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

type Rule struct {
	Limit  int64
	Window time.Duration
}

type Decision struct {
	Allowed    bool
	RetryAfter time.Duration
}

type store interface {
	Increment(context.Context, string, time.Duration) (int64, time.Duration, error)
	Close() error
}

type Limiter struct {
	store    store
	secret   []byte
	prefix   string
	disabled bool
}

func newLimiter(store store, secret []byte, prefix string) *Limiter {
	return &Limiter{
		store:  store,
		secret: append([]byte(nil), secret...),
		prefix: strings.TrimSuffix(prefix, ":"),
	}
}

func NewDisabled() *Limiter {
	return &Limiter{disabled: true}
}

func (l *Limiter) Allow(
	ctx context.Context,
	scope string,
	subject string,
	rule Rule,
) (Decision, error) {
	if l == nil {
		return Decision{}, fmt.Errorf("rate limiter is nil")
	}
	if l.disabled {
		return Decision{Allowed: true}, nil
	}
	if l.store == nil || len(l.secret) == 0 {
		return Decision{}, fmt.Errorf("rate limiter is not initialized")
	}
	if !validScope(scope) || strings.TrimSpace(subject) == "" {
		return Decision{}, fmt.Errorf("rate limit scope and subject are required")
	}
	if rule.Limit <= 0 || rule.Window <= 0 {
		return Decision{}, fmt.Errorf("rate limit rule must be positive")
	}

	digest := hmac.New(sha256.New, l.secret)
	_, _ = digest.Write([]byte(subject))
	key := l.prefix + ":" + scope + ":" + hex.EncodeToString(digest.Sum(nil))
	count, retryAfter, err := l.store.Increment(ctx, key, rule.Window)
	if err != nil {
		return Decision{}, fmt.Errorf("increment rate limit: %w", err)
	}
	if count <= rule.Limit {
		return Decision{Allowed: true}, nil
	}
	if retryAfter <= 0 {
		retryAfter = rule.Window
	}
	return Decision{RetryAfter: retryAfter}, nil
}

func (l *Limiter) Close() error {
	if l == nil || l.store == nil {
		return nil
	}
	return l.store.Close()
}

func validScope(scope string) bool {
	if scope == "" {
		return false
	}
	for _, character := range scope {
		if character >= 'a' && character <= 'z' ||
			character >= '0' && character <= '9' ||
			character == '-' || character == '_' || character == ':' {
			continue
		}
		return false
	}
	return true
}
