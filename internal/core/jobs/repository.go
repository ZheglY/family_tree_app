package jobs

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Repository interface {
	Enqueue(context.Context, EnqueueRequest) (Job, bool, error)
	Claim(context.Context, string, time.Duration, time.Time) (*Job, error)
	Heartbeat(context.Context, uuid.UUID, string, time.Duration, time.Time) error
	Succeed(context.Context, uuid.UUID, string, time.Time) error
	Fail(context.Context, uuid.UUID, string, string, time.Time, time.Time) (FailureResult, error)
}
