package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/ZheglY/family_tree_app/internal/core/jobs"
	jobpostgres "github.com/ZheglY/family_tree_app/internal/core/jobs/postgres"
	"github.com/ZheglY/family_tree_app/internal/core/logger"
	"github.com/ZheglY/family_tree_app/internal/testdatabase"
	"github.com/ZheglY/family_tree_app/migrations"
	"github.com/google/uuid"
)

func TestRepositoryClaimLeaseRetryAndDeadState(t *testing.T) {
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
	repository := jobpostgres.New(database.Pool)
	now := time.Date(2026, time.August, 21, 12, 0, 0, 0, time.UTC)
	request := jobs.EnqueueRequest{
		ID:               uuid.New(),
		Kind:             "test.job",
		DeduplicationKey: "aggregate-1",
		Payload:          json.RawMessage(`{"pending_before":"2026-08-20T00:00:00Z","batch_size":100}`),
		MaxAttempts:      2,
		AvailableAt:      now,
		CreatedAt:        now,
	}
	enqueued, created, err := repository.Enqueue(ctx, request)
	if err != nil || !created || enqueued.Status != jobs.StatusQueued {
		t.Fatalf("Enqueue() job = %#v, created = %t, error = %v", enqueued, created, err)
	}
	repeated, created, err := repository.Enqueue(ctx, request)
	if err != nil || created || repeated.ID != enqueued.ID {
		t.Fatalf("repeated Enqueue() job = %#v, created = %t, error = %v", repeated, created, err)
	}
	conflict := request
	conflict.ID = uuid.New()
	conflict.Payload = json.RawMessage(`{"pending_before":"different","batch_size":100}`)
	if _, _, err := repository.Enqueue(ctx, conflict); !errors.Is(err, jobs.ErrDeduplicationConflict) {
		t.Fatalf("conflicting Enqueue() error = %v", err)
	}

	claimed, err := repository.Claim(ctx, "worker-a", 30*time.Second, now)
	if err != nil || claimed == nil || claimed.ID != enqueued.ID || claimed.Attempts != 1 ||
		claimed.Status != jobs.StatusRunning {
		t.Fatalf("first Claim() job = %#v, error = %v", claimed, err)
	}
	if unavailable, err := repository.Claim(ctx, "worker-b", 30*time.Second, now.Add(time.Second)); err != nil || unavailable != nil {
		t.Fatalf("active lease Claim() job = %#v, error = %v", unavailable, err)
	}
	if err := repository.Heartbeat(ctx, claimed.ID, "worker-a", 30*time.Second, now.Add(10*time.Second)); err != nil {
		t.Fatalf("Heartbeat() error = %v", err)
	}
	reclaimed, err := repository.Claim(ctx, "worker-b", 30*time.Second, now.Add(41*time.Second))
	if err != nil || reclaimed == nil || reclaimed.ID != claimed.ID || reclaimed.Attempts != 2 {
		t.Fatalf("expired lease Claim() job = %#v, error = %v", reclaimed, err)
	}
	if err := repository.Succeed(ctx, claimed.ID, "worker-a", now.Add(42*time.Second)); !errors.Is(err, jobs.ErrLeaseLost) {
		t.Fatalf("stale worker Succeed() error = %v", err)
	}
	failure, err := repository.Fail(
		ctx,
		reclaimed.ID,
		"worker-b",
		"permanent failure",
		now.Add(time.Minute),
		now.Add(42*time.Second),
	)
	if err != nil || !failure.Dead || failure.Status != jobs.StatusDead {
		t.Fatalf("final Fail() result = %#v, error = %v", failure, err)
	}
	if noJob, err := repository.Claim(ctx, "worker-c", 30*time.Second, now.Add(2*time.Minute)); err != nil || noJob != nil {
		t.Fatalf("dead job Claim() job = %#v, error = %v", noJob, err)
	}
}

func TestRepositoryUsesSkipLockedForConcurrentClaims(t *testing.T) {
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
	defer database.Close()
	runner, err := migrations.NewRunner(database.Pool, logger.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Up(ctx); err != nil {
		t.Fatal(err)
	}
	repository := jobpostgres.New(database.Pool)
	now := time.Now().UTC()
	for index := 0; index < 2; index++ {
		_, _, err := repository.Enqueue(ctx, jobs.EnqueueRequest{
			ID: uuid.New(), Kind: "concurrent.job", Payload: json.RawMessage(`{"ok":true}`),
			MaxAttempts: 3, AvailableAt: now, CreatedAt: now.Add(time.Duration(index) * time.Millisecond),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	type result struct {
		job *jobs.Job
		err error
	}
	results := make(chan result, 2)
	for _, workerID := range []string{"worker-a", "worker-b"} {
		go func(id string) {
			job, err := repository.Claim(ctx, id, time.Minute, now.Add(time.Second))
			results <- result{job: job, err: err}
		}(workerID)
	}
	first, second := <-results, <-results
	if first.err != nil || second.err != nil || first.job == nil || second.job == nil ||
		first.job.ID == second.job.ID {
		t.Fatalf("concurrent claims = %#v and %#v", first, second)
	}
}
