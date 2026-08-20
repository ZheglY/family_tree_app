package ratelimit

import (
	"context"
	"crypto/rand"
	"fmt"
	"strings"
	"time"

	"github.com/kelseyhightower/envconfig"
)

const minimumSharedSecretBytes = 32

type Config struct {
	Enabled       bool          `envconfig:"ENABLED" default:"true"`
	Backend       string        `envconfig:"BACKEND" default:"memory"`
	KeyPrefix     string        `envconfig:"KEY_PREFIX" default:"family-tree:auth"`
	KeySecret     string        `envconfig:"KEY_SECRET"`
	MemoryMaxKeys int           `envconfig:"MEMORY_MAX_KEYS" default:"10000"`
	RedisAddress  string        `envconfig:"REDIS_ADDR" default:"localhost:6379"`
	RedisUsername string        `envconfig:"REDIS_USERNAME"`
	RedisPassword string        `envconfig:"REDIS_PASSWORD"`
	RedisDB       int           `envconfig:"REDIS_DB" default:"0"`
	RedisTimeout  time.Duration `envconfig:"REDIS_TIMEOUT" default:"500ms"`
}

func LoadConfig() (Config, error) {
	var config Config
	if err := envconfig.Process("AUTH_RATE_LIMIT", &config); err != nil {
		return Config{}, fmt.Errorf("process auth rate limit config: %w", err)
	}
	config.Backend = strings.ToLower(strings.TrimSpace(config.Backend))
	config.KeyPrefix = strings.Trim(strings.TrimSpace(config.KeyPrefix), ":")
	if !config.Enabled {
		return config, nil
	}
	if config.KeyPrefix == "" {
		return Config{}, fmt.Errorf("AUTH_RATE_LIMIT_KEY_PREFIX is required")
	}
	if config.MemoryMaxKeys <= 0 {
		return Config{}, fmt.Errorf("AUTH_RATE_LIMIT_MEMORY_MAX_KEYS must be positive")
	}
	if config.RedisTimeout <= 0 {
		return Config{}, fmt.Errorf("AUTH_RATE_LIMIT_REDIS_TIMEOUT must be positive")
	}
	if config.RedisDB < 0 {
		return Config{}, fmt.Errorf("AUTH_RATE_LIMIT_REDIS_DB must not be negative")
	}

	switch config.Backend {
	case "memory":
	case "redis":
		if strings.TrimSpace(config.RedisAddress) == "" {
			return Config{}, fmt.Errorf("AUTH_RATE_LIMIT_REDIS_ADDR is required")
		}
		if len(config.KeySecret) < minimumSharedSecretBytes {
			return Config{}, fmt.Errorf(
				"AUTH_RATE_LIMIT_KEY_SECRET must contain at least %d bytes for Redis",
				minimumSharedSecretBytes,
			)
		}
	default:
		return Config{}, fmt.Errorf("unsupported auth rate limit backend %q", config.Backend)
	}
	return config, nil
}

func New(ctx context.Context, config Config) (*Limiter, error) {
	if !config.Enabled {
		return NewDisabled(), nil
	}

	secret := []byte(config.KeySecret)
	if len(secret) == 0 {
		secret = make([]byte, minimumSharedSecretBytes)
		if _, err := rand.Read(secret); err != nil {
			return nil, fmt.Errorf("generate rate limit key secret: %w", err)
		}
	}

	var backend store
	switch config.Backend {
	case "memory":
		backend = newMemoryStore(config.MemoryMaxKeys)
	case "redis":
		redisBackend, err := newRedisStore(ctx, config)
		if err != nil {
			return nil, err
		}
		backend = redisBackend
	default:
		return nil, fmt.Errorf("unsupported auth rate limit backend %q", config.Backend)
	}
	return newLimiter(backend, secret, config.KeyPrefix), nil
}
