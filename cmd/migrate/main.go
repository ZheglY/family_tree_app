package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/ZheglY/family_tree_app/internal/core/logger"
	"github.com/ZheglY/family_tree_app/internal/core/postgres"
	"github.com/ZheglY/family_tree_app/migrations"
)

func main() {
	ctx, stop := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "migration failed:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, arguments []string) error {
	postgresConfig, err := postgres.LoadConfig()
	if err != nil {
		return err
	}
	log, err := logger.NewLogger(logger.NewConfigMust())
	if err != nil {
		return fmt.Errorf("initialize logger: %w", err)
	}
	defer log.Close()
	database, err := postgres.Open(ctx, postgresConfig, log)
	if err != nil {
		return err
	}
	defer database.Close()
	runner, err := migrations.NewRunner(database.Native(), log)
	if err != nil {
		return fmt.Errorf("initialize migration runner: %w", err)
	}

	action := "up"
	if len(arguments) > 0 {
		action = arguments[0]
	}
	switch action {
	case "up":
		return runner.Up(ctx)
	case "down":
		steps := 1
		if len(arguments) > 1 {
			steps, err = strconv.Atoi(arguments[1])
			if err != nil || steps <= 0 {
				return fmt.Errorf("down steps must be a positive integer")
			}
		}
		return runner.Down(ctx, steps)
	case "version":
		version, err := runner.CurrentVersion(ctx)
		if err != nil {
			return err
		}
		fmt.Println(version)
		return nil
	default:
		return fmt.Errorf("unknown action %q; use up, down [steps], or version", action)
	}
}
