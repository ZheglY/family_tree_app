package logger

import (
	"fmt"
	"strings"

	"github.com/kelseyhightower/envconfig"
)

type LoggerConfig struct {
	Level  string `envconfig:"LOGGER_LEVEL" default:"DEBUG"`
	Folder string `envconfig:"LOGGER_FOLDER" default:"./logs"`
	Format string `envconfig:"LOGGER_FORMAT" default:"json"`
}

func NewConfig() (LoggerConfig, error) {
	var config LoggerConfig
	if err := envconfig.Process("", &config); err != nil {
		return LoggerConfig{}, fmt.Errorf("envconfig problem: %w", err)
	}
	config.Format = strings.ToLower(strings.TrimSpace(config.Format))
	if config.Format != "json" && config.Format != "console" {
		return LoggerConfig{}, fmt.Errorf("LOGGER_FORMAT must be json or console")
	}
	return config, nil
}

func NewConfigMust() LoggerConfig {
	loggerConfig, err := NewConfig()
	if err != nil {
		err = fmt.Errorf("logger config creation error: %w", err)
		panic(err)
	}

	return loggerConfig
}
