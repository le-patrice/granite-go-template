// Command seed creates the first admin account from SEED_ADMIN_EMAIL and
// SEED_ADMIN_PASSWORD, and refuses to run when an admin already exists.
package main

import (
	"context"
	"errors"
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

	email := os.Getenv("SEED_ADMIN_EMAIL")
	password := os.Getenv("SEED_ADMIN_PASSWORD")
	if email == "" || password == "" {
		err := errors.New("SEED_ADMIN_EMAIL and SEED_ADMIN_PASSWORD must both be set")
		logger.Error("cannot seed admin", "error", err)
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := app.SeedAdmin(ctx, cfg, logger, email, password); err != nil {
		logger.ErrorContext(ctx, "seed failed", "error", err)
		return err
	}
	return nil
}
