package worker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ZheglY/family_tree_app/internal/core/jobs"
	"github.com/ZheglY/family_tree_app/internal/core/logger"
	"go.uber.org/zap"
)

type Handler interface {
	Handle(context.Context, jobs.Job) error
}

type HandlerFunc func(context.Context, jobs.Job) error

func (handler HandlerFunc) Handle(ctx context.Context, job jobs.Job) error {
	return handler(ctx, job)
}

type Worker struct {
	repository jobs.Repository
	handlers   map[string]Handler
	config     Config
	log        *logger.Logger
	now        func() time.Time
}

func New(
	repository jobs.Repository,
	handlers map[string]Handler,
	config Config,
	log *logger.Logger,
) (*Worker, error) {
	if repository == nil || len(handlers) == 0 || log == nil || config.ID == "" ||
		config.PollInterval <= 0 || config.LeaseDuration <= 0 ||
		config.HeartbeatInterval <= 0 || config.HeartbeatInterval*2 >= config.LeaseDuration ||
		config.RetryBaseDelay <= 0 || config.RetryMaxDelay < config.RetryBaseDelay ||
		config.AckTimeout <= 0 {
		return nil, fmt.Errorf("initialize worker: %w", jobs.ErrInvalidJob)
	}
	for kind, handler := range handlers {
		if kind == "" || handler == nil {
			return nil, fmt.Errorf("initialize worker handler: %w", jobs.ErrInvalidJob)
		}
	}
	return &Worker{
		repository: repository,
		handlers:   handlers,
		config:     config,
		log:        log,
		now: func() time.Time {
			return time.Now().UTC()
		},
	}, nil
}

func (worker *Worker) Run(ctx context.Context) error {
	worker.log.Info("background worker started", zap.String("worker_id", worker.config.ID))
	defer worker.log.Info("background worker stopped", zap.String("worker_id", worker.config.ID))
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		job, err := worker.repository.Claim(
			ctx,
			worker.config.ID,
			worker.config.LeaseDuration,
			worker.now(),
		)
		if err != nil {
			worker.log.Error("claim background job", zap.Error(err))
			if !wait(ctx, worker.config.PollInterval) {
				return nil
			}
			continue
		}
		if job == nil {
			if !wait(ctx, worker.config.PollInterval) {
				return nil
			}
			continue
		}
		worker.process(ctx, *job)
	}
}

func (worker *Worker) process(parent context.Context, job jobs.Job) {
	log := worker.log.With(
		zap.String("job_id", job.ID.String()),
		zap.String("job_kind", job.Kind),
		zap.Int("job_attempt", job.Attempts),
	)
	jobContext, cancelJob := context.WithCancel(parent)
	stopHeartbeat := make(chan struct{})
	heartbeatDone := make(chan error, 1)
	go worker.heartbeat(jobContext, cancelJob, job, stopHeartbeat, heartbeatDone)

	handler, found := worker.handlers[job.Kind]
	var handleError error
	if !found {
		handleError = fmt.Errorf("no handler registered for job kind %q", job.Kind)
	} else {
		handleError = callHandler(jobContext, handler, job)
	}
	close(stopHeartbeat)
	heartbeatError := <-heartbeatDone
	cancelJob()
	if heartbeatError != nil {
		log.Warn("background job lease lost", zap.Error(heartbeatError))
		return
	}
	if parent.Err() != nil && errors.Is(handleError, context.Canceled) {
		return
	}

	ackContext, cancelAck := context.WithTimeout(context.WithoutCancel(parent), worker.config.AckTimeout)
	defer cancelAck()
	if handleError == nil {
		if err := worker.repository.Succeed(ackContext, job.ID, worker.config.ID, worker.now()); err != nil {
			log.Warn("acknowledge successful background job", zap.Error(err))
			return
		}
		log.Info("background job succeeded")
		return
	}

	now := worker.now()
	availableAt := now.Add(worker.retryDelay(job.Attempts))
	result, err := worker.repository.Fail(
		ackContext,
		job.ID,
		worker.config.ID,
		handleError.Error(),
		availableAt,
		now,
	)
	if err != nil {
		log.Warn("record background job failure", zap.Error(err))
		return
	}
	if result.Dead {
		log.Error("background job moved to dead state", zap.Error(handleError))
		return
	}
	log.Warn(
		"background job scheduled for retry",
		zap.Error(handleError),
		zap.Time("available_at", result.Available),
	)
}

func callHandler(ctx context.Context, handler Handler, job jobs.Job) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("background job handler panic: %v", recovered)
		}
	}()
	return handler.Handle(ctx, job)
}

func (worker *Worker) heartbeat(
	ctx context.Context,
	cancelJob context.CancelFunc,
	job jobs.Job,
	stop <-chan struct{},
	done chan<- error,
) {
	ticker := time.NewTicker(worker.config.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			done <- nil
			return
		case <-ctx.Done():
			done <- nil
			return
		case <-ticker.C:
			heartbeatContext, cancel := context.WithTimeout(
				context.WithoutCancel(ctx),
				worker.config.AckTimeout,
			)
			err := worker.repository.Heartbeat(
				heartbeatContext,
				job.ID,
				worker.config.ID,
				worker.config.LeaseDuration,
				worker.now(),
			)
			cancel()
			if err != nil {
				cancelJob()
				done <- err
				return
			}
		}
	}
}

func (worker *Worker) retryDelay(attempt int) time.Duration {
	delay := worker.config.RetryBaseDelay
	for current := 1; current < attempt && delay < worker.config.RetryMaxDelay; current++ {
		if delay > worker.config.RetryMaxDelay/2 {
			return worker.config.RetryMaxDelay
		}
		delay *= 2
	}
	if delay > worker.config.RetryMaxDelay {
		return worker.config.RetryMaxDelay
	}
	return delay
}

func wait(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
