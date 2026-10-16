// Command api serves the HTTP API.
package main

import (
	"context"
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.InfoContext(ctx, "starting api", "config", cfg)
	if err := app.RunAPI(ctx, cfg, logger); err != nil {
		logger.ErrorContext(ctx, "api failed", "error", err)
		return err
	}
	return nil
}
