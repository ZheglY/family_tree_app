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
	"github.com/ZheglY/family_tree_app/internal/features/exports/exportjob"
	exportprocessing "github.com/ZheglY/family_tree_app/internal/features/exports/processing"
	exportpostgres "github.com/ZheglY/family_tree_app/internal/features/exports/repository/postgres"
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
	exportConfig, err := exportprocessing.LoadConfig()
	if err != nil {
		return err
	}
	jobRepository := jobpostgres.New(database.Native())
	mediaRepository := mediapostgres.New(database.Native())
	exportRepository := exportpostgres.New(database.Native())
	runner, err := jobworker.New(
		jobRepository,
		map[string]jobworker.Handler{
			mediajob.KindProcess: processing.NewProcessor(mediaRepository, objectStorage),
			mediajob.KindCleanup: processing.NewCleanup(mediaRepository, objectStorage),
			exportjob.KindGenerate: exportprocessing.NewGenerator(
				exportRepository,
				objectStorage,
				exportConfig.ResultTTL,
				exportConfig.MaxArchiveBytes,
			),
			exportjob.KindCleanup: exportprocessing.NewCleanup(exportRepository, objectStorage),
			exportjob.KindDelete:  exportprocessing.NewDeleter(exportRepository, objectStorage),
		},
		workerConfig,
		log,
	)
	if err != nil {
		return err
	}
	if err := enqueueMediaCleanup(ctx, jobRepository, cleanupConfig, time.Now().UTC()); err != nil {
		return err
	}
	if err := enqueueExportCleanup(ctx, jobRepository, exportConfig, time.Now().UTC()); err != nil {
		return err
	}
	go scheduleMediaCleanup(ctx, jobRepository, cleanupConfig, log)
	go scheduleExportCleanup(ctx, jobRepository, exportConfig, log)
	return runner.Run(ctx)
}

func scheduleMediaCleanup(
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
			if err := enqueueMediaCleanup(ctx, repository, config, now.UTC()); err != nil {
				log.Error("schedule media cleanup", zap.Error(err))
			}
		}
	}
}

func enqueueMediaCleanup(
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

func scheduleExportCleanup(
	ctx context.Context,
	repository jobs.Repository,
	config exportprocessing.Config,
	log *logger.Logger,
) {
	ticker := time.NewTicker(config.CleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if err := enqueueExportCleanup(ctx, repository, config, now.UTC()); err != nil {
				log.Error("schedule export cleanup", zap.Error(err))
			}
		}
	}
}

func enqueueExportCleanup(
	ctx context.Context,
	repository jobs.Repository,
	config exportprocessing.Config,
	now time.Time,
) error {
	scheduledAt := now.Truncate(config.CleanupInterval)
	payload, err := exportjob.Encode(exportjob.CleanupPayload{
		ExpiredBefore: scheduledAt,
		BatchSize:     config.CleanupBatchSize,
	})
	if err != nil {
		return err
	}
	_, _, err = repository.Enqueue(ctx, jobs.EnqueueRequest{
		ID:               uuid.New(),
		Kind:             exportjob.KindCleanup,
		DeduplicationKey: scheduledAt.Format(time.RFC3339),
		Payload:          payload,
		MaxAttempts:      5,
		AvailableAt:      scheduledAt,
		CreatedAt:        scheduledAt,
	})
	if err != nil {
		return fmt.Errorf("enqueue export cleanup: %w", err)
	}
	return nil
}
