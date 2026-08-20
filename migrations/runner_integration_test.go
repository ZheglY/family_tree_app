package migrations_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/ZheglY/family_tree_app/internal/core/logger"
	"github.com/ZheglY/family_tree_app/internal/testdatabase"
	"github.com/ZheglY/family_tree_app/migrations"
)

func TestRunnerRollsPersonMigrationDownAndBackUp(t *testing.T) {
	databaseURL := os.Getenv("FAMILY_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("FAMILY_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	database, err := testdatabase.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
	}()
	runner, err := migrations.NewRunner(database.Pool, logger.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Up(ctx); err != nil {
		t.Fatalf("Up() error = %v", err)
	}
	if err := runner.Down(ctx, 1); err != nil {
		t.Fatalf("Down() error = %v", err)
	}
	if version, err := runner.CurrentVersion(ctx); err != nil || version != 1 {
		t.Fatalf("version after down = %d, error = %v", version, err)
	}
	var personsMissing, treesPresent bool
	if err := database.Pool.QueryRow(ctx, `
		SELECT to_regclass('persons') IS NULL,
		       to_regclass('family_trees') IS NOT NULL
	`).Scan(&personsMissing, &treesPresent); err != nil {
		t.Fatal(err)
	}
	if !personsMissing || !treesPresent {
		t.Fatalf("schema after down: persons missing = %t, trees present = %t", personsMissing, treesPresent)
	}
	if err := runner.Up(ctx); err != nil {
		t.Fatalf("second Up() error = %v", err)
	}
	if version, err := runner.CurrentVersion(ctx); err != nil || version != 2 {
		t.Fatalf("final version = %d, error = %v", version, err)
	}
}
