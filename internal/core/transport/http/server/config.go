package server

import (
	"time"
	"fmt"

	"github.com/kelseyhightower/envconfig"
)

/* Конфиг для создания сервера
Addr - адрес
ShutDownTimeout - ?
*/
type Config struct {
	Addr            string `envconfig:"ADDR" required:"true"`
	ShutDownTimeout time.Duration `envconfig:"SHUTDOWN_TIMEOUT" default:"30s"`
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
	
	return config, nil
}

func NewConfigMust() (Config) {
	config, err := NewConfig()
	if err != nil {
		err = fmt.Errorf("get HTTP server config %w", err)
		panic(err)
	}

	return config
}