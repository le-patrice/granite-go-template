// Command worker runs the River job worker. "worker migrate" applies or
// removes River's own migrations.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"example.com/app/internal/app"
	"example.com/app/internal/config"
	"example.com/app/internal/platform/telemetry"
)

func main() {
	if err := run(); err != nil {
		os.Exit(1)
	}
}

func run() error {
	bootstrap := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		return runMigrate(bootstrap, os.Args[2:])
	}

	cfg, err := config.Load()
	if err != nil {
		bootstrap.Error("invalid configuration", "error", err)
		return err
	}
	logger, err := telemetry.NewLogger(os.Stdout, cfg.LogLevel, cfg.ServiceName, cfg.Env)
	if err != nil {
		bootstrap.Error("invalid logger setup", "error", err)
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.InfoContext(ctx, "starting worker", "config", cfg)
	if err := app.RunWorker(ctx, cfg, logger); err != nil {
		logger.ErrorContext(ctx, "worker failed", "error", err)
		return err
	}
	return nil
}

func runMigrate(logger *slog.Logger, args []string) error {
	flags := flag.NewFlagSet("migrate", flag.ContinueOnError)
	direction := flags.String("direction", "up", "up or down")
	url := flags.String("url", "", "database URL (default: DIRECT_DATABASE_URL, then DATABASE_URL)")
	if err := flags.Parse(args); err != nil {
		return err
	}

	target := *url
	if target == "" {
		cfg, err := config.Load()
		if err != nil {
			logger.Error("invalid configuration", "error", err)
			return err
		}
		target = cfg.DirectDatabaseURL
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := app.RunMigrate(ctx, target, *direction, logger); err != nil {
		logger.ErrorContext(ctx, "migration failed", "error", err)
		return err
	}
	return nil
}
