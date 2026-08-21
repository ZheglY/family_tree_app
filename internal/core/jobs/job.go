package jobs

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusFailed    = "failed"
	StatusSucceeded = "succeeded"
	StatusDead      = "dead"
)

var (
	ErrInvalidJob            = errors.New("invalid background job")
	ErrLeaseLost             = errors.New("background job lease was lost")
	ErrDeduplicationConflict = errors.New("background job deduplication conflict")
)

type Job struct {
	ID               uuid.UUID
	Kind             string
	DeduplicationKey *string
	Payload          json.RawMessage
	Status           string
	Attempts         int
	MaxAttempts      int
	AvailableAt      time.Time
	LeaseExpiresAt   *time.Time
	HeartbeatAt      *time.Time
	LockedBy         *string
	LastError        string
	CreatedAt        time.Time
	UpdatedAt        time.Time
	CompletedAt      *time.Time
}

type EnqueueRequest struct {
	ID               uuid.UUID
	Kind             string
	DeduplicationKey string
	Payload          json.RawMessage
	MaxAttempts      int
	AvailableAt      time.Time
	CreatedAt        time.Time
}

func (request EnqueueRequest) Validate() error {
	request.Kind = strings.TrimSpace(request.Kind)
	request.DeduplicationKey = strings.TrimSpace(request.DeduplicationKey)
	if request.ID == uuid.Nil || request.Kind == "" || len(request.Kind) > 100 ||
		(request.DeduplicationKey != "" && len(request.DeduplicationKey) > 255) ||
		request.MaxAttempts < 1 || request.MaxAttempts > 100 ||
		request.AvailableAt.IsZero() || request.CreatedAt.IsZero() ||
		!json.Valid(request.Payload) {
		return ErrInvalidJob
	}
	return nil
}

type FailureResult struct {
	Dead      bool
	Status    string
	Available time.Time
}
