package testdatabase

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Database struct {
	Pool       *pgxpool.Pool
	adminPool  *pgxpool.Pool
	schemaName string
}

func Open(ctx context.Context, databaseURL string) (*Database, error) {
	adminConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse test database URL: %w", err)
	}
	if !strings.Contains(strings.ToLower(adminConfig.ConnConfig.Database), "test") {
		return nil, fmt.Errorf(
			"refusing to use non-test database %q",
			adminConfig.ConnConfig.Database,
		)
	}

	adminPool, err := pgxpool.NewWithConfig(ctx, adminConfig)
	if err != nil {
		return nil, fmt.Errorf("create test database admin pool: %w", err)
	}
	schemaName := "test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quotedSchema := pgx.Identifier{schemaName}.Sanitize()
	if _, err := adminPool.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		adminPool.Close()
		return nil, fmt.Errorf("create isolated test schema: %w", err)
	}

	testConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		adminPool.Close()
		return nil, fmt.Errorf("parse isolated pool config: %w", err)
	}
	testConfig.AfterConnect = func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, "SET search_path TO "+quotedSchema)
		return err
	}
	testPool, err := pgxpool.NewWithConfig(ctx, testConfig)
	if err != nil {
		_, _ = adminPool.Exec(ctx, "DROP SCHEMA "+quotedSchema+" CASCADE")
		adminPool.Close()
		return nil, fmt.Errorf("create isolated test pool: %w", err)
	}
	return &Database{
		Pool:       testPool,
		adminPool:  adminPool,
		schemaName: schemaName,
	}, nil
}

func (d *Database) Close() error {
	if d == nil {
		return nil
	}
	if d.Pool != nil {
		d.Pool.Close()
	}
	if d.adminPool == nil {
		return nil
	}
	defer d.adminPool.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	quotedSchema := pgx.Identifier{d.schemaName}.Sanitize()
	if _, err := d.adminPool.Exec(ctx, "DROP SCHEMA "+quotedSchema+" CASCADE"); err != nil {
		return fmt.Errorf("drop isolated test schema: %w", err)
	}
	return nil
}
