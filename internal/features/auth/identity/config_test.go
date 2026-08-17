package identity

import (
	"os"
	"testing"
	"time"
)

func TestLoadConfigUsesDevelopmentDefaults(t *testing.T) {
	for _, key := range []string{
		"IDENTITY_GRPC_ADDR",
		"IDENTITY_GRPC_TIMEOUT",
		"IDENTITY_GRPC_TLS_ENABLED",
		"IDENTITY_GRPC_TLS_SERVER_NAME",
		"IDENTITY_GRPC_CA_FILE",
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

	config, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if config.Address != "localhost:50051" || config.Timeout != 3*time.Second {
		t.Fatalf("config = %#v", config)
	}
}

func TestLoadConfigRejectsNonPositiveTimeout(t *testing.T) {
	t.Setenv("IDENTITY_GRPC_TIMEOUT", "0s")
	if _, err := LoadConfig(); err == nil {
		t.Fatal("LoadConfig() error = nil, want error")
	}
}
