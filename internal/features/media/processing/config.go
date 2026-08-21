package processing

import (
	"fmt"
	"time"

	"github.com/kelseyhightower/envconfig"
)

type CleanupConfig struct {
	Interval         time.Duration `envconfig:"INTERVAL" default:"1h"`
	PendingTTL       time.Duration `envconfig:"PENDING_TTL" default:"24h"`
	DeletedRetention time.Duration `envconfig:"DELETED_RETENTION" default:"720h"`
	BatchSize        int           `envconfig:"BATCH_SIZE" default:"100"`
}

func LoadCleanupConfig() (CleanupConfig, error) {
	var config CleanupConfig
	if err := envconfig.Process("MEDIA_CLEANUP", &config); err != nil {
		return CleanupConfig{}, fmt.Errorf("process media cleanup config: %w", err)
	}
	if config.Interval <= 0 || config.PendingTTL <= 0 ||
		config.DeletedRetention <= 0 || config.BatchSize < 1 || config.BatchSize > 1000 {
		return CleanupConfig{}, fmt.Errorf("media cleanup config is invalid")
	}
	return config, nil
}
