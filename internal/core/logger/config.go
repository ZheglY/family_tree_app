package logger

import (
	"fmt"

	"github.com/kelseyhightower/envconfig"
)

type LoggerConfig struct {
	Level  string `envconfig:"LOGGER_LEVEL" default:"DEBUG"`
	Folder string `envconfig:"LOGGER_FOLDER" default:"./logs"`
}

func NewConfig() (LoggerConfig, error) {
	var config LoggerConfig
	if err := envconfig.Process("", &config); err != nil {
		return LoggerConfig{}, fmt.Errorf("envconfig problem: %w", err)
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
