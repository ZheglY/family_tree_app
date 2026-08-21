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

func TestRunnerRollsLatestMigrationDownAndBackUp(t *testing.T) {
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
	if version, err := runner.CurrentVersion(ctx); err != nil || version != 8 {
		t.Fatalf("version after down = %d, error = %v", version, err)
	}
	var exportsPresent, jobsPresent bool
	if err := database.Pool.QueryRow(ctx, `
		SELECT to_regclass('export_jobs') IS NOT NULL,
		       to_regclass('background_jobs') IS NOT NULL
	`).Scan(&exportsPresent, &jobsPresent); err != nil {
		t.Fatal(err)
	}
	if !exportsPresent || !jobsPresent {
		t.Fatalf(
			"schema after down: exports present = %t, jobs present = %t",
			exportsPresent,
			jobsPresent,
		)
	}
	if err := runner.Up(ctx); err != nil {
		t.Fatalf("second Up() error = %v", err)
	}
	if version, err := runner.CurrentVersion(ctx); err != nil || version != 9 {
		t.Fatalf("final version = %d, error = %v", version, err)
	}
}
