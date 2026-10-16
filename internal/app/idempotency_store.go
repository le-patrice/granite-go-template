package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"example.com/app/internal/platform/idempotency"
	"example.com/app/internal/store/sqlc"
)

// idempotencyStore implements idempotency.Store with the sqlc queries. It
// lives in app because platform packages may not import internal/store/sqlc.
type idempotencyStore struct {
	q *sqlc.Queries
}

var _ idempotency.Store = (*idempotencyStore)(nil)

func newIdempotencyStore(pool *pgxpool.Pool) *idempotencyStore {
	return &idempotencyStore{q: sqlc.New(pool)}
}

func (s *idempotencyStore) Reserve(ctx context.Context, scope, key string, requestHash []byte) (bool, error) {
	n, err := s.q.ReserveIdempotencyKey(ctx, sqlc.ReserveIdempotencyKeyParams{
		Scope:       scope,
		Key:         key,
		RequestHash: requestHash,
	})
	if err != nil {
		return false, fmt.Errorf("idempotency: reserve: %w", err)
	}
	return n == 1, nil
}

func (s *idempotencyStore) Get(ctx context.Context, scope, key string) (idempotency.Record, error) {
	row, err := s.q.GetIdempotencyKey(ctx, sqlc.GetIdempotencyKeyParams{Scope: scope, Key: key})
	if errors.Is(err, pgx.ErrNoRows) {
		return idempotency.Record{}, idempotency.ErrNotFound
	}
	if err != nil {
		return idempotency.Record{}, fmt.Errorf("idempotency: get: %w", err)
	}
	rec := idempotency.Record{RequestHash: row.RequestHash, Body: row.ResponseBody}
	if row.Status.Valid {
		rec.Status = int(row.Status.Int32)
	}
	return rec, nil
}

func (s *idempotencyStore) Complete(ctx context.Context, scope, key string, status int, body []byte) error {
	err := s.q.CompleteIdempotencyKey(ctx, sqlc.CompleteIdempotencyKeyParams{
		Scope:        scope,
		Key:          key,
		Status:       pgtype.Int4{Int32: int32(status), Valid: true},
		ResponseBody: body,
	})
	if err != nil {
		return fmt.Errorf("idempotency: complete: %w", err)
	}
	return nil
}

func (s *idempotencyStore) Release(ctx context.Context, scope, key string) error {
	if err := s.q.DeleteIdempotencyKey(ctx, sqlc.DeleteIdempotencyKeyParams{Scope: scope, Key: key}); err != nil {
		return fmt.Errorf("idempotency: release: %w", err)
	}
	return nil
}

func (s *idempotencyStore) DeleteExpired(ctx context.Context, cutoff time.Time) (int64, error) {
	n, err := s.q.DeleteExpiredIdempotencyKeys(ctx, cutoff)
	if err != nil {
		return 0, fmt.Errorf("idempotency: delete expired: %w", err)
	}
	return n, nil
}
