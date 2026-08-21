package worker

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kelseyhightower/envconfig"
)

type Config struct {
	ID                string        `envconfig:"ID"`
	PollInterval      time.Duration `envconfig:"POLL_INTERVAL" default:"1s"`
	LeaseDuration     time.Duration `envconfig:"LEASE_DURATION" default:"30s"`
	HeartbeatInterval time.Duration `envconfig:"HEARTBEAT_INTERVAL" default:"10s"`
	RetryBaseDelay    time.Duration `envconfig:"RETRY_BASE_DELAY" default:"2s"`
	RetryMaxDelay     time.Duration `envconfig:"RETRY_MAX_DELAY" default:"15m"`
	AckTimeout        time.Duration `envconfig:"ACK_TIMEOUT" default:"5s"`
}

func LoadConfig() (Config, error) {
	var config Config
	if err := envconfig.Process("WORKER", &config); err != nil {
		return Config{}, fmt.Errorf("process worker config: %w", err)
	}
	if strings.TrimSpace(config.ID) == "" {
		hostname, err := os.Hostname()
		if err != nil || strings.TrimSpace(hostname) == "" {
			hostname = "worker"
		}
		config.ID = fmt.Sprintf("%s-%s", hostname, uuid.NewString())
	}
	if len(config.ID) > 255 || config.PollInterval <= 0 ||
		config.LeaseDuration <= 0 || config.HeartbeatInterval <= 0 ||
		config.HeartbeatInterval*2 >= config.LeaseDuration ||
		config.RetryBaseDelay <= 0 || config.RetryMaxDelay < config.RetryBaseDelay ||
		config.AckTimeout <= 0 {
		return Config{}, fmt.Errorf("worker durations or identifier are invalid")
	}
	return config, nil
}
