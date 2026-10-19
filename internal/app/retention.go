package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"example.com/app/internal/store/sqlc"
)

// KindRetention is the River job kind of the periodic retention sweep.
const KindRetention = "maintenance.retention"

// retentionEvery is how often the sweep is scheduled. River elects one leader
// across all worker replicas, so the sweep is enqueued once per tick, not once
// per replica.
const retentionEvery = time.Hour

// RetentionArgs is the argument-less periodic retention job.
type RetentionArgs struct{}

// Kind implements river.JobArgs.
func (RetentionArgs) Kind() string { return KindRetention }

// retentionPolicy holds how long each kind of row is kept.
type retentionPolicy struct {
	// IdempotencyKeys is how long a stored idempotency key can be replayed.
	IdempotencyKeys time.Duration
	// InboundEvents must exceed the longest sender retry window: once an event
	// row is deleted, a late redelivery of the same event is processed again.
	InboundEvents time.Duration
	// RefreshTokenGrace is how long an expired refresh token is kept, so reuse
	// detection still recognises it shortly after expiry.
	RefreshTokenGrace time.Duration
}

// retentionStore is the set of deletes the sweep needs. *sqlc.Queries satisfies it.
type retentionStore interface {
	DeleteExpiredIdempotencyKeys(ctx context.Context, cutoff time.Time) (int64, error)
	DeleteExpiredRefreshTokens(ctx context.Context, cutoff time.Time) (int64, error)
	DeleteOldInboundEvents(ctx context.Context, cutoff time.Time) (int64, error)
}

var _ retentionStore = (*sqlc.Queries)(nil)

type retentionWorker struct {
	river.WorkerDefaults[RetentionArgs]
	store  retentionStore
	policy retentionPolicy
	now    func() time.Time
	logger *slog.Logger
}

func newRetentionWorker(pool *pgxpool.Pool, policy retentionPolicy, logger *slog.Logger) *retentionWorker {
	return &retentionWorker{store: sqlc.New(pool), policy: policy, now: time.Now, logger: logger}
}

// Work deletes expired rows. Every delete runs even if another fails, and the
// job fails (so River retries) if any did. Deleting is idempotent.
func (w *retentionWorker) Work(ctx context.Context, _ *river.Job[RetentionArgs]) error {
	now := w.now().UTC()
	var errs []error
	for _, sweep := range []struct {
		name   string
		cutoff time.Time
		run    func(context.Context, time.Time) (int64, error)
	}{
		{"idempotency_keys", now.Add(-w.policy.IdempotencyKeys), w.store.DeleteExpiredIdempotencyKeys},
		{"refresh_tokens", now.Add(-w.policy.RefreshTokenGrace), w.store.DeleteExpiredRefreshTokens},
		{"inbound_events", now.Add(-w.policy.InboundEvents), w.store.DeleteOldInboundEvents},
	} {
		deleted, err := sweep.run(ctx, sweep.cutoff)
		if err != nil {
			errs = append(errs, fmt.Errorf("retention: %s: %w", sweep.name, err))
			continue
		}
		if deleted > 0 {
			w.logger.InfoContext(ctx, "retention sweep deleted rows", "table", sweep.name, "deleted", deleted)
		}
	}
	return errors.Join(errs...)
}

// retentionPeriodicJob schedules the sweep, once at startup and then hourly.
func retentionPeriodicJob() *river.PeriodicJob {
	return river.NewPeriodicJob(
		river.PeriodicInterval(retentionEvery),
		func() (river.JobArgs, *river.InsertOpts) {
			return RetentionArgs{}, &river.InsertOpts{
				MaxAttempts: 3,
				UniqueOpts:  river.UniqueOpts{ByPeriod: retentionEvery},
			}
		},
		&river.PeriodicJobOpts{RunOnStart: true},
	)
}
