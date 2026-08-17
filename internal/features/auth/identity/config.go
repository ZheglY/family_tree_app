package identity

import (
	"fmt"
	"time"

	"github.com/kelseyhightower/envconfig"
)

type Config struct {
	Address       string        `envconfig:"GRPC_ADDR" default:"localhost:50051"`
	Timeout       time.Duration `envconfig:"GRPC_TIMEOUT" default:"3s"`
	TLSEnabled    bool          `envconfig:"GRPC_TLS_ENABLED" default:"false"`
	TLSServerName string        `envconfig:"GRPC_TLS_SERVER_NAME"`
	TLSCAFile     string        `envconfig:"GRPC_CA_FILE"`
}

func LoadConfig() (Config, error) {
	var config Config
	if err := envconfig.Process("IDENTITY", &config); err != nil {
		return Config{}, fmt.Errorf("process Identity client config: %w", err)
	}
	if config.Address == "" {
		return Config{}, fmt.Errorf("IDENTITY_GRPC_ADDR is required")
	}
	if config.Timeout <= 0 {
		return Config{}, fmt.Errorf("IDENTITY_GRPC_TIMEOUT must be positive")
	}

	return config, nil
}
