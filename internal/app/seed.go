package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"example.com/app/internal/config"
	"example.com/app/internal/platform/store"
	"example.com/app/internal/users"
)

// SeedAdmin creates the first admin account. If an admin already exists, it
// logs and succeeds as a no-op so repeated bootstrap and container startups are
// idempotent. It uses the direct database URL because it is a one-off command.
func SeedAdmin(ctx context.Context, cfg config.Config, logger *slog.Logger, email, password string) error {
	pool, err := store.NewPool(ctx, store.PoolConfig{URL: cfg.DirectDatabaseURL})
	if err != nil {
		return fmt.Errorf("app: %w", err)
	}
	defer pool.Close()

	ids, err := newIdentity(cfg, pool, logger)
	if err != nil {
		return fmt.Errorf("app: identity: %w", err)
	}
	user, err := ids.users.CreateAdmin(ctx, email, password)
	if errors.Is(err, users.ErrAdminExists) {
		logger.InfoContext(ctx, "admin already exists, skipping seed")
		return nil
	}
	if err != nil {
		return fmt.Errorf("app: seed admin: %w", err)
	}
	logger.InfoContext(ctx, "admin created", "user_id", user.ID.String())
	return nil
}
