package migrations

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"time"

	"github.com/ZheglY/family_tree_app/internal/core/logger"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

const migrationAdvisoryLockID int64 = 8_146_732_905

type Runner struct {
	pool       *pgxpool.Pool
	migrations []Migration
	log        *logger.Logger
}

type appliedMigration struct {
	Version  int64
	Name     string
	Checksum string
}

func NewRunner(pool *pgxpool.Pool, log *logger.Logger) (*Runner, error) {
	return newRunner(pool, log, Files)
}

func newRunner(pool *pgxpool.Pool, log *logger.Logger, files fs.FS) (*Runner, error) {
	loaded, err := Load(files)
	if err != nil {
		return nil, err
	}
	return &Runner{pool: pool, migrations: loaded, log: log}, nil
}

func (r *Runner) Up(ctx context.Context) error {
	var appliedNow []Migration
	err := r.inLockedTransaction(ctx, func(tx pgx.Tx) error {
		applied, err := loadAppliedMigrations(ctx, tx)
		if err != nil {
			return err
		}
		if err := r.validateApplied(applied); err != nil {
			return err
		}
		appliedByVersion := make(map[int64]struct{}, len(applied))
		for _, migration := range applied {
			appliedByVersion[migration.Version] = struct{}{}
		}
		for _, migration := range r.migrations {
			if _, exists := appliedByVersion[migration.Version]; exists {
				continue
			}
			if _, err := tx.Exec(ctx, migration.UpSQL); err != nil {
				return fmt.Errorf(
					"apply migration %d_%s: %w",
					migration.Version,
					migration.Name,
					err,
				)
			}
			if _, err := tx.Exec(
				ctx,
				`INSERT INTO schema_migrations (version, name, checksum) VALUES ($1, $2, $3)`,
				migration.Version,
				migration.Name,
				migration.Checksum,
			); err != nil {
				return fmt.Errorf("record migration %d: %w", migration.Version, err)
			}
			appliedNow = append(appliedNow, migration)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, migration := range appliedNow {
		r.log.Info(
			"migration applied",
			zap.Int64("version", migration.Version),
			zap.String("name", migration.Name),
		)
	}
	return nil
}

func (r *Runner) Down(ctx context.Context, steps int) error {
	if steps <= 0 {
		return fmt.Errorf("migration down steps must be positive")
	}
	return r.inLockedTransaction(ctx, func(tx pgx.Tx) error {
		applied, err := loadAppliedMigrations(ctx, tx)
		if err != nil {
			return err
		}
		if err := r.validateApplied(applied); err != nil {
			return err
		}
		byVersion := make(map[int64]Migration, len(r.migrations))
		for _, migration := range r.migrations {
			byVersion[migration.Version] = migration
		}
		sort.Slice(applied, func(i, j int) bool {
			return applied[i].Version > applied[j].Version
		})
		if steps > len(applied) {
			steps = len(applied)
		}
		for _, appliedMigration := range applied[:steps] {
			migration := byVersion[appliedMigration.Version]
			if _, err := tx.Exec(ctx, migration.DownSQL); err != nil {
				return fmt.Errorf(
					"rollback migration %d_%s: %w",
					migration.Version,
					migration.Name,
					err,
				)
			}
			if _, err := tx.Exec(
				ctx,
				"DELETE FROM schema_migrations WHERE version = $1",
				migration.Version,
			); err != nil {
				return fmt.Errorf("remove migration record %d: %w", migration.Version, err)
			}
		}
		return nil
	})
}

func (r *Runner) CurrentVersion(ctx context.Context) (int64, error) {
	var version int64
	err := r.inLockedTransaction(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(
			ctx,
			"SELECT COALESCE(MAX(version), 0) FROM schema_migrations",
		).Scan(&version)
	})
	return version, err
}

func (r *Runner) inLockedTransaction(
	ctx context.Context,
	operation func(pgx.Tx) error,
) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin migration transaction: %w", err)
	}
	defer func() {
		rollbackContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(rollbackContext)
	}()
	if _, err := tx.Exec(
		ctx,
		"SELECT pg_advisory_xact_lock($1)",
		migrationAdvisoryLockID,
	); err != nil {
		return fmt.Errorf("acquire migration advisory lock: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version bigint PRIMARY KEY,
			name text NOT NULL,
			checksum text NOT NULL,
			applied_at timestamptz NOT NULL DEFAULT now()
		)
	`); err != nil {
		return fmt.Errorf("ensure schema_migrations table: %w", err)
	}
	if err := operation(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit migration transaction: %w", err)
	}
	return nil
}

func loadAppliedMigrations(
	ctx context.Context,
	tx pgx.Tx,
) ([]appliedMigration, error) {
	rows, err := tx.Query(
		ctx,
		"SELECT version, name, checksum FROM schema_migrations ORDER BY version",
	)
	if err != nil {
		return nil, fmt.Errorf("query applied migrations: %w", err)
	}
	defer rows.Close()
	var applied []appliedMigration
	for rows.Next() {
		var migration appliedMigration
		if err := rows.Scan(
			&migration.Version,
			&migration.Name,
			&migration.Checksum,
		); err != nil {
			return nil, fmt.Errorf("scan applied migration: %w", err)
		}
		applied = append(applied, migration)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate applied migrations: %w", err)
	}
	return applied, nil
}

func (r *Runner) validateApplied(applied []appliedMigration) error {
	known := make(map[int64]Migration, len(r.migrations))
	for _, migration := range r.migrations {
		known[migration.Version] = migration
	}
	for _, appliedMigration := range applied {
		migration, exists := known[appliedMigration.Version]
		if !exists {
			return fmt.Errorf(
				"database contains unknown migration version %d",
				appliedMigration.Version,
			)
		}
		if migration.Name != appliedMigration.Name ||
			migration.Checksum != appliedMigration.Checksum {
			return fmt.Errorf(
				"applied migration %d differs from embedded migration",
				appliedMigration.Version,
			)
		}
	}
	return nil
}
