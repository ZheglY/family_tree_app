package postgres

import (
	"fmt"
	"strings"
	"time"

	"github.com/kelseyhightower/envconfig"
)

type Config struct {
	URL               string        `envconfig:"URL" default:"postgres://family_tree:family_tree@localhost:5434/family_tree?sslmode=disable"`
	MaxConnections    int32         `envconfig:"MAX_CONNECTIONS" default:"10"`
	MinConnections    int32         `envconfig:"MIN_CONNECTIONS" default:"1"`
	MaxConnLifetime   time.Duration `envconfig:"MAX_CONN_LIFETIME" default:"30m"`
	MaxConnIdleTime   time.Duration `envconfig:"MAX_CONN_IDLE_TIME" default:"5m"`
	HealthCheckPeriod time.Duration `envconfig:"HEALTH_CHECK_PERIOD" default:"15s"`
	ConnectTimeout    time.Duration `envconfig:"CONNECT_TIMEOUT" default:"5s"`
}

func LoadConfig() (Config, error) {
	var config Config
	if err := envconfig.Process("POSTGRES", &config); err != nil {
		return Config{}, fmt.Errorf("process PostgreSQL config: %w", err)
	}
	if strings.TrimSpace(config.URL) == "" {
		return Config{}, fmt.Errorf("POSTGRES_URL is required")
	}
	if config.MaxConnections <= 0 || config.MinConnections < 0 ||
		config.MinConnections > config.MaxConnections {
		return Config{}, fmt.Errorf("PostgreSQL pool bounds are invalid")
	}
	if config.MaxConnLifetime <= 0 || config.MaxConnIdleTime <= 0 ||
		config.HealthCheckPeriod <= 0 || config.ConnectTimeout <= 0 {
		return Config{}, fmt.Errorf("PostgreSQL durations must be positive")
	}
	return config, nil
}
