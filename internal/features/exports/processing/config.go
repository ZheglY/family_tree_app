package processing

import (
	"fmt"
	"time"

	"github.com/kelseyhightower/envconfig"
)

type Config struct {
	ResultTTL        time.Duration `envconfig:"RESULT_TTL" default:"168h"`
	CleanupInterval  time.Duration `envconfig:"CLEANUP_INTERVAL" default:"1h"`
	CleanupBatchSize int           `envconfig:"CLEANUP_BATCH_SIZE" default:"100"`
	MaxArchiveBytes  int64         `envconfig:"MAX_ARCHIVE_BYTES" default:"268435456"`
}

func LoadConfig() (Config, error) {
	var config Config
	if err := envconfig.Process("EXPORT", &config); err != nil {
		return Config{}, fmt.Errorf("process export config: %w", err)
	}
	if config.ResultTTL <= 0 || config.CleanupInterval <= 0 ||
		config.CleanupBatchSize < 1 || config.CleanupBatchSize > 1000 ||
		config.MaxArchiveBytes < 1024*1024 {
		return Config{}, fmt.Errorf("export config is invalid")
	}
	return config, nil
}
