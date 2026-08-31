package observability

import (
	"fmt"
	"strings"
	"time"

	"github.com/kelseyhightower/envconfig"
)

type Config struct {
	Enabled           bool          `envconfig:"ENABLED" default:"true"`
	Addr              string        `envconfig:"ADDR"`
	ReadHeaderTimeout time.Duration `envconfig:"READ_HEADER_TIMEOUT" default:"5s"`
	ShutdownTimeout   time.Duration `envconfig:"SHUTDOWN_TIMEOUT" default:"5s"`
}

func LoadConfig(prefix string, defaultAddr string) (Config, error) {
	config := Config{Addr: defaultAddr}
	if err := envconfig.Process(prefix, &config); err != nil {
		return Config{}, fmt.Errorf("process %s config: %w", prefix, err)
	}
	config.Addr = strings.TrimSpace(config.Addr)
	if config.Enabled && config.Addr == "" {
		return Config{}, fmt.Errorf("%s_ADDR is required when metrics are enabled", prefix)
	}
	if config.ReadHeaderTimeout <= 0 || config.ShutdownTimeout <= 0 {
		return Config{}, fmt.Errorf("%s timeouts must be positive", prefix)
	}
	return config, nil
}
