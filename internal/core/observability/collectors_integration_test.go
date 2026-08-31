package observability

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ZheglY/family_tree_app/internal/core/jobs"
	jobpostgres "github.com/ZheglY/family_tree_app/internal/core/jobs/postgres"
	"github.com/ZheglY/family_tree_app/internal/core/logger"
	"github.com/ZheglY/family_tree_app/internal/testdatabase"
	"github.com/ZheglY/family_tree_app/migrations"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestJobQueueCollectorAgainstPostgreSQL(t *testing.T) {
	databaseURL := os.Getenv("FAMILY_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("FAMILY_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
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
		t.Fatalf("apply migrations: %v", err)
	}
	now := time.Now().UTC()
	_, _, err = jobpostgres.New(database.Pool).Enqueue(ctx, jobs.EnqueueRequest{
		ID:          uuid.New(),
		Kind:        "test.observe",
		Payload:     json.RawMessage(`{}`),
		MaxAttempts: 3,
		AvailableAt: now,
		CreatedAt:   now,
	})
	if err != nil {
		t.Fatalf("enqueue job: %v", err)
	}
	metrics, err := NewMetrics(database.Pool)
	if err != nil {
		t.Fatalf("create metrics: %v", err)
	}
	expected := `
# HELP family_tree_job_queue_jobs Current background jobs by state.
# TYPE family_tree_job_queue_jobs gauge
family_tree_job_queue_jobs{status="dead"} 0
family_tree_job_queue_jobs{status="failed"} 0
family_tree_job_queue_jobs{status="queued"} 1
family_tree_job_queue_jobs{status="running"} 0
family_tree_job_queue_jobs{status="succeeded"} 0
`
	if err := testutil.GatherAndCompare(
		metrics.registry,
		strings.NewReader(expected),
		"family_tree_job_queue_jobs",
	); err != nil {
		t.Fatalf("compare queue metrics: %v", err)
	}
}
