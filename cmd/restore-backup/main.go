package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/ZheglY/family_tree_app/internal/core/logger"
	corepostgres "github.com/ZheglY/family_tree_app/internal/core/postgres"
	s3storage "github.com/ZheglY/family_tree_app/internal/core/storage/s3"
	"github.com/ZheglY/family_tree_app/internal/features/exports/processing"
	"github.com/ZheglY/family_tree_app/internal/features/exports/restore"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "backup restore failed:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, arguments []string) error {
	if len(arguments) != 1 {
		return fmt.Errorf("usage: restore-backup <family-tree-backup.zip>")
	}
	exportConfig, err := processing.LoadConfig()
	if err != nil {
		return err
	}
	body, err := readLimited(arguments[0], exportConfig.MaxArchiveBytes)
	if err != nil {
		return err
	}
	archive, err := restore.ParseZIP(body, exportConfig.MaxArchiveBytes)
	if err != nil {
		return err
	}
	log, err := logger.NewLogger(logger.NewConfigMust())
	if err != nil {
		return fmt.Errorf("initialize restore logger: %w", err)
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
	storageConfig, err := s3storage.LoadConfig()
	if err != nil {
		return err
	}
	objectStore, err := s3storage.New(ctx, storageConfig)
	if err != nil {
		return fmt.Errorf("initialize restore S3 storage: %w", err)
	}
	if err := objectStore.EnsureBucket(ctx); err != nil {
		return fmt.Errorf("ensure restore S3 bucket: %w", err)
	}
	result, err := restore.NewRestorer(database.Native(), objectStore).Restore(ctx, archive)
	if err != nil {
		return err
	}
	fmt.Printf(
		"restored tree %s: objects=%d processing_jobs=%d\n",
		result.TreeID,
		result.ObjectsRestored,
		result.JobsEnqueued,
	)
	return nil
}

func readLimited(filename string, maxBytes int64) ([]byte, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("open backup archive: %w", err)
	}
	defer file.Close()
	body, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read backup archive: %w", err)
	}
	if int64(len(body)) > maxBytes {
		return nil, restore.ErrBackupTooLarge
	}
	return body, nil
}
