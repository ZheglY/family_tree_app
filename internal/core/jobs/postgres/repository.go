package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ZheglY/family_tree_app/internal/core/jobs"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const maxStoredErrorRunes = 1000

type Repository struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

type queryExecutor interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

// EnqueueWith inserts a job through either a pool or an existing transaction.
// Callers use the transaction form when a domain state change and its job must
// become visible atomically.
func EnqueueWith(
	ctx context.Context,
	executor queryExecutor,
	request jobs.EnqueueRequest,
) (jobs.Job, bool, error) {
	if err := request.Validate(); err != nil {
		return jobs.Job{}, false, err
	}
	kind := strings.TrimSpace(request.Kind)
	var deduplicationKey any
	if value := strings.TrimSpace(request.DeduplicationKey); value != "" {
		deduplicationKey = value
	}
	result, err := executor.Exec(ctx, `
		INSERT INTO background_jobs (
			id, kind, deduplication_key, payload, status, attempts,
			max_attempts, available_at, lease_expires_at, heartbeat_at,
			locked_by, last_error, created_at, updated_at, completed_at
		) VALUES (
			$1, $2, $3, $4, 'queued', 0,
			$5, $6, NULL, NULL,
			NULL, '', $7, $7, NULL
		)
		ON CONFLICT (kind, deduplication_key)
			WHERE deduplication_key IS NOT NULL
		DO NOTHING
	`,
		request.ID,
		kind,
		deduplicationKey,
		request.Payload,
		request.MaxAttempts,
		request.AvailableAt,
		request.CreatedAt,
	)
	if err != nil {
		return jobs.Job{}, false, fmt.Errorf("enqueue background job: %w", err)
	}
	created := result.RowsAffected() == 1
	var job jobs.Job
	if created {
		job, err = scanJob(executor.QueryRow(ctx, jobSelect+` WHERE id = $1`, request.ID))
	} else {
		job, err = scanJob(executor.QueryRow(ctx, jobSelect+`
			WHERE kind = $1 AND deduplication_key = $2
		`, kind, deduplicationKey))
	}
	if err != nil {
		return jobs.Job{}, false, fmt.Errorf("load enqueued background job: %w", err)
	}
	if !created && (!bytes.Equal(canonicalJSON(job.Payload), canonicalJSON(request.Payload)) ||
		job.MaxAttempts != request.MaxAttempts) {
		return jobs.Job{}, false, jobs.ErrDeduplicationConflict
	}
	return job, created, nil
}

func (r *Repository) Enqueue(
	ctx context.Context,
	request jobs.EnqueueRequest,
) (jobs.Job, bool, error) {
	return EnqueueWith(ctx, r.pool, request)
}

func (r *Repository) Claim(
	ctx context.Context,
	workerID string,
	leaseDuration time.Duration,
	now time.Time,
) (*jobs.Job, error) {
	workerID = strings.TrimSpace(workerID)
	if workerID == "" || len(workerID) > 255 || leaseDuration <= 0 || now.IsZero() {
		return nil, jobs.ErrInvalidJob
	}
	leaseExpiresAt := now.Add(leaseDuration)
	row := r.pool.QueryRow(ctx, `
		WITH exhausted AS (
			UPDATE background_jobs
			SET status = 'dead',
				lease_expires_at = NULL,
				heartbeat_at = NULL,
				locked_by = NULL,
				last_error = CASE
					WHEN last_error = '' THEN 'worker lease expired after final attempt'
					ELSE last_error
				END,
				updated_at = $2,
				completed_at = $2
			WHERE status = 'running'
			  AND lease_expires_at <= $2
			  AND attempts >= max_attempts
		), candidate AS (
			SELECT id
			FROM background_jobs
			WHERE attempts < max_attempts
			  AND (
				(status IN ('queued', 'failed') AND available_at <= $2) OR
				(status = 'running' AND lease_expires_at <= $2)
			  )
			ORDER BY available_at, created_at, id
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		), claimed AS (
			UPDATE background_jobs job
			SET status = 'running',
				attempts = attempts + 1,
				lease_expires_at = $3,
				heartbeat_at = $2,
				locked_by = $1,
				updated_at = $2,
				completed_at = NULL
			FROM candidate
			WHERE job.id = candidate.id
			RETURNING job.*
		)
		SELECT
			id, kind, deduplication_key, payload, status, attempts,
			max_attempts, available_at, lease_expires_at, heartbeat_at,
			locked_by, last_error, created_at, updated_at, completed_at
		FROM claimed
	`, workerID, now, leaseExpiresAt)
	job, err := scanJob(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claim background job: %w", err)
	}
	return &job, nil
}

func (r *Repository) Heartbeat(
	ctx context.Context,
	jobID uuid.UUID,
	workerID string,
	leaseDuration time.Duration,
	now time.Time,
) error {
	if jobID == uuid.Nil || strings.TrimSpace(workerID) == "" || leaseDuration <= 0 || now.IsZero() {
		return jobs.ErrInvalidJob
	}
	result, err := r.pool.Exec(ctx, `
		UPDATE background_jobs
		SET heartbeat_at = $3,
			lease_expires_at = $4,
			updated_at = $3
		WHERE id = $1
		  AND status = 'running'
		  AND locked_by = $2
		  AND lease_expires_at > $3
	`, jobID, workerID, now, now.Add(leaseDuration))
	if err != nil {
		return fmt.Errorf("heartbeat background job: %w", err)
	}
	if result.RowsAffected() != 1 {
		return jobs.ErrLeaseLost
	}
	return nil
}

func (r *Repository) Succeed(
	ctx context.Context,
	jobID uuid.UUID,
	workerID string,
	now time.Time,
) error {
	if jobID == uuid.Nil || strings.TrimSpace(workerID) == "" || now.IsZero() {
		return jobs.ErrInvalidJob
	}
	result, err := r.pool.Exec(ctx, `
		UPDATE background_jobs
		SET status = 'succeeded',
			lease_expires_at = NULL,
			heartbeat_at = NULL,
			locked_by = NULL,
			last_error = '',
			updated_at = $3,
			completed_at = $3
		WHERE id = $1
		  AND status = 'running'
		  AND locked_by = $2
		  AND lease_expires_at > $3
	`, jobID, workerID, now)
	if err != nil {
		return fmt.Errorf("complete background job: %w", err)
	}
	if result.RowsAffected() != 1 {
		return jobs.ErrLeaseLost
	}
	return nil
}

func (r *Repository) Fail(
	ctx context.Context,
	jobID uuid.UUID,
	workerID string,
	errorMessage string,
	availableAt time.Time,
	now time.Time,
) (jobs.FailureResult, error) {
	if jobID == uuid.Nil || strings.TrimSpace(workerID) == "" ||
		availableAt.IsZero() || now.IsZero() {
		return jobs.FailureResult{}, jobs.ErrInvalidJob
	}
	errorMessage = truncate(strings.TrimSpace(errorMessage), maxStoredErrorRunes)
	var result jobs.FailureResult
	err := r.pool.QueryRow(ctx, `
		UPDATE background_jobs
		SET status = CASE WHEN attempts >= max_attempts THEN 'dead' ELSE 'failed' END,
			available_at = CASE WHEN attempts >= max_attempts THEN available_at ELSE $4::timestamptz END,
			lease_expires_at = NULL,
			heartbeat_at = NULL,
			locked_by = NULL,
			last_error = $3,
			updated_at = $5::timestamptz,
			completed_at = CASE WHEN attempts >= max_attempts THEN $5::timestamptz ELSE NULL END
		WHERE id = $1
		  AND status = 'running'
		  AND locked_by = $2
		  AND lease_expires_at > $5::timestamptz
		RETURNING status, available_at
	`, jobID, workerID, errorMessage, availableAt, now).Scan(&result.Status, &result.Available)
	if errors.Is(err, pgx.ErrNoRows) {
		return jobs.FailureResult{}, jobs.ErrLeaseLost
	}
	if err != nil {
		return jobs.FailureResult{}, fmt.Errorf("fail background job: %w", err)
	}
	result.Dead = result.Status == jobs.StatusDead
	return result, nil
}

const jobSelect = `
	SELECT
		id, kind, deduplication_key, payload, status, attempts,
		max_attempts, available_at, lease_expires_at, heartbeat_at,
		locked_by, last_error, created_at, updated_at, completed_at
	FROM background_jobs
`

type scanner interface {
	Scan(...any) error
}

func scanJob(row scanner) (jobs.Job, error) {
	var job jobs.Job
	err := row.Scan(
		&job.ID,
		&job.Kind,
		&job.DeduplicationKey,
		&job.Payload,
		&job.Status,
		&job.Attempts,
		&job.MaxAttempts,
		&job.AvailableAt,
		&job.LeaseExpiresAt,
		&job.HeartbeatAt,
		&job.LockedBy,
		&job.LastError,
		&job.CreatedAt,
		&job.UpdatedAt,
		&job.CompletedAt,
	)
	return job, err
}

func canonicalJSON(value json.RawMessage) []byte {
	var decoded any
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return value
	}
	canonical, err := json.Marshal(decoded)
	if err != nil {
		return value
	}
	return canonical
}

func truncate(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}
