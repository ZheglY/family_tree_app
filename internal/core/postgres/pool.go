package postgres

import (
	"context"
	"fmt"

	"github.com/ZheglY/family_tree_app/internal/core/logger"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

type Pool struct {
	pool *pgxpool.Pool
	log  *logger.Logger
}

func Open(ctx context.Context, config Config, log *logger.Logger) (*Pool, error) {
	poolConfig, err := pgxpool.ParseConfig(config.URL)
	if err != nil {
		return nil, fmt.Errorf("parse PostgreSQL config: %w", err)
	}
	poolConfig.MaxConns = config.MaxConnections
	poolConfig.MinConns = config.MinConnections
	poolConfig.MaxConnLifetime = config.MaxConnLifetime
	poolConfig.MaxConnIdleTime = config.MaxConnIdleTime
	poolConfig.HealthCheckPeriod = config.HealthCheckPeriod
	poolConfig.ConnConfig.ConnectTimeout = config.ConnectTimeout

	nativePool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("create PostgreSQL pool: %w", err)
	}
	pool := &Pool{pool: nativePool, log: log}
	if err := pool.Ping(ctx); err != nil {
		nativePool.Close()
		return nil, fmt.Errorf("connect PostgreSQL: %w", err)
	}
	log.Info(
		"PostgreSQL connection established",
		zap.Int32("max_connections", config.MaxConnections),
		zap.Int32("min_connections", config.MinConnections),
	)
	return pool, nil
}

func (p *Pool) Ping(ctx context.Context) error {
	if p == nil || p.pool == nil {
		return fmt.Errorf("PostgreSQL pool is not initialized")
	}
	if err := p.pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping PostgreSQL: %w", err)
	}
	return nil
}

func (p *Pool) Native() *pgxpool.Pool {
	return p.pool
}

func (p *Pool) Close() {
	if p == nil || p.pool == nil {
		return
	}
	p.pool.Close()
	if p.log != nil {
		p.log.Info("PostgreSQL connection pool closed")
	}
}
