package server

import (
	"fmt"
	"time"

	"github.com/kelseyhightower/envconfig"
)

/*
	Конфиг для создания сервера

Addr - адрес
ShutDownTimeout - ?
*/
type Config struct {
	Addr              string        `envconfig:"ADDR" default:":8080"`
	ShutDownTimeout   time.Duration `envconfig:"SHUTDOWN_TIMEOUT" default:"30s"`
	ReadHeaderTimeout time.Duration `envconfig:"READ_HEADER_TIMEOUT" default:"5s"`
	ReadTimeout       time.Duration `envconfig:"READ_TIMEOUT" default:"15s"`
	WriteTimeout      time.Duration `envconfig:"WRITE_TIMEOUT" default:"30s"`
	IdleTimeout       time.Duration `envconfig:"IDLE_TIMEOUT" default:"60s"`
	MaxBodyBytes      int64         `envconfig:"MAX_BODY_BYTES" default:"1048576"`
}

func NewConfig() (Config, error) {
	var config Config

	/*
		Библиотека envconfig читает переменные окружения и
		записывает их значения в поля структуры config.
	*/
	if err := envconfig.Process("HTTP", &config); err != nil {
		return Config{}, fmt.Errorf("process envconfig: %w", err)
	}

	if config.ShutDownTimeout <= 0 ||
		config.ReadHeaderTimeout <= 0 ||
		config.ReadTimeout <= 0 ||
		config.WriteTimeout <= 0 ||
		config.IdleTimeout <= 0 {
		return Config{}, fmt.Errorf("HTTP timeouts must be positive")
	}

	if config.MaxBodyBytes <= 0 {
		return Config{}, fmt.Errorf("HTTP max body bytes must be positive")
	}

	return config, nil
}

func NewConfigMust() Config {
	config, err := NewConfig()
	if err != nil {
		err = fmt.Errorf("get HTTP server config %w", err)
		panic(err)
	}

	return config
}
