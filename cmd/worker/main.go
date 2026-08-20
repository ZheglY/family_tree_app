package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ZheglY/family_tree_app/internal/core/jobs"
	jobpostgres "github.com/ZheglY/family_tree_app/internal/core/jobs/postgres"
	jobworker "github.com/ZheglY/family_tree_app/internal/core/jobs/worker"
	"github.com/ZheglY/family_tree_app/internal/core/logger"
	corepostgres "github.com/ZheglY/family_tree_app/internal/core/postgres"
	s3storage "github.com/ZheglY/family_tree_app/internal/core/storage/s3"
	"github.com/ZheglY/family_tree_app/internal/features/media/mediajob"
	"github.com/ZheglY/family_tree_app/internal/features/media/processing"
	mediapostgres "github.com/ZheglY/family_tree_app/internal/features/media/repository/postgres"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "worker failed:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	log, err := logger.NewLogger(logger.NewConfigMust())
	if err != nil {
		return fmt.Errorf("initialize worker logger: %w", err)
	}
	defer log.Close()
	postgresConfig, err := corepostgres.LoadConfig()
	if err != nil {
		return err
	}
	database, err := corepostgres.Open(ctx, postgresConfig, log)
	if err != nil {
		return err
	}
	defer database.Close()
	objectStorageConfig, err := s3storage.LoadConfig()
	if err != nil {
		return err
	}
	objectStorage, err := s3storage.New(ctx, objectStorageConfig)
	if err != nil {
		return fmt.Errorf("initialize worker S3 storage: %w", err)
	}
	if err := objectStorage.EnsureBucket(ctx); err != nil {
		return fmt.Errorf("ensure worker S3 bucket: %w", err)
	}
	workerConfig, err := jobworker.LoadConfig()
	if err != nil {
		return err
	}
	cleanupConfig, err := processing.LoadCleanupConfig()
	if err != nil {
		return err
	}
	jobRepository := jobpostgres.New(database.Native())
	mediaRepository := mediapostgres.New(database.Native())
	runner, err := jobworker.New(
		jobRepository,
		map[string]jobworker.Handler{
			mediajob.KindProcess: processing.NewProcessor(mediaRepository, objectStorage),
			mediajob.KindCleanup: processing.NewCleanup(mediaRepository, objectStorage),
		},
		workerConfig,
		log,
	)
	if err != nil {
		return err
	}
	if err := enqueueCleanup(ctx, jobRepository, cleanupConfig, time.Now().UTC()); err != nil {
		return err
	}
	go scheduleCleanup(ctx, jobRepository, cleanupConfig, log)
	return runner.Run(ctx)
}

func scheduleCleanup(
	ctx context.Context,
	repository jobs.Repository,
	config processing.CleanupConfig,
	log *logger.Logger,
) {
	ticker := time.NewTicker(config.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if err := enqueueCleanup(ctx, repository, config, now.UTC()); err != nil {
				log.Error("schedule media cleanup", zap.Error(err))
			}
		}
	}
}

func enqueueCleanup(
	ctx context.Context,
	repository jobs.Repository,
	config processing.CleanupConfig,
	now time.Time,
) error {
	scheduledAt := now.Truncate(config.Interval)
	payload, err := mediajob.Encode(mediajob.CleanupPayload{
		PendingBefore: scheduledAt.Add(-config.PendingTTL),
		DeletedBefore: scheduledAt.Add(-config.DeletedRetention),
		BatchSize:     config.BatchSize,
	})
	if err != nil {
		return err
	}
	deduplicationBucket := scheduledAt.Format(time.RFC3339)
	_, _, err = repository.Enqueue(ctx, jobs.EnqueueRequest{
		ID:               uuid.New(),
		Kind:             mediajob.KindCleanup,
		DeduplicationKey: deduplicationBucket,
		Payload:          payload,
		MaxAttempts:      5,
		AvailableAt:      scheduledAt,
		CreatedAt:        scheduledAt,
	})
	if err != nil {
		return fmt.Errorf("enqueue media cleanup: %w", err)
	}
	return nil
}
