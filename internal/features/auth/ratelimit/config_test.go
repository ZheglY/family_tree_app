package ratelimit

import (
	"os"
	"testing"
)

func TestLoadConfigUsesMemoryDefaults(t *testing.T) {
	unsetRateLimitEnvironment(t)
	config, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if !config.Enabled || config.Backend != "memory" || config.MemoryMaxKeys != 10000 {
		t.Fatalf("config = %#v", config)
	}
}

func TestLoadConfigRequiresSharedSecretForRedis(t *testing.T) {
	unsetRateLimitEnvironment(t)
	t.Setenv("AUTH_RATE_LIMIT_BACKEND", "redis")
	t.Setenv("AUTH_RATE_LIMIT_KEY_SECRET", "short")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("LoadConfig() error = nil, want shared secret error")
	}
}

func unsetRateLimitEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"AUTH_RATE_LIMIT_ENABLED",
		"AUTH_RATE_LIMIT_BACKEND",
		"AUTH_RATE_LIMIT_KEY_PREFIX",
		"AUTH_RATE_LIMIT_KEY_SECRET",
		"AUTH_RATE_LIMIT_MEMORY_MAX_KEYS",
		"AUTH_RATE_LIMIT_REDIS_ADDR",
		"AUTH_RATE_LIMIT_REDIS_USERNAME",
		"AUTH_RATE_LIMIT_REDIS_PASSWORD",
		"AUTH_RATE_LIMIT_REDIS_DB",
		"AUTH_RATE_LIMIT_REDIS_TIMEOUT",
	} {
		value, exists := os.LookupEnv(key)
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("Unsetenv(%s) error = %v", key, err)
		}
		t.Cleanup(func() {
			if exists {
				_ = os.Setenv(key, value)
			} else {
				_ = os.Unsetenv(key)
			}
		})
	}
}
