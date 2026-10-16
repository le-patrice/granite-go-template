package orders

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"example.com/app/internal/platform/store"
	"example.com/app/internal/store/sqlc"
)

const referenceConstraint = "orders_owner_id_reference_key"

// JobInserter enqueues a job inside an existing transaction. The insert-only
// *river.Client[pgx.Tx] satisfies it.
type JobInserter interface {
	InsertTx(ctx context.Context, tx pgx.Tx, args river.JobArgs, opts *river.InsertOpts) (*rivertype.JobInsertResult, error)
}

// Repo implements Repository with sqlc and River.
type Repo struct {
	pool *pgxpool.Pool
	q    *sqlc.Queries
	jobs JobInserter
}

// NewRepo returns a Repo. jobs may be nil for read-only use (the worker);
// CreateWithEvent then returns an error.
func NewRepo(pool *pgxpool.Pool, jobs JobInserter) *Repo {
	return &Repo{pool: pool, q: sqlc.New(pool), jobs: jobs}
}

// CreateWithEvent inserts the order and its order.created job in one
// transaction (pgx.BeginFunc via store.InTx plus River InsertTx).
func (r *Repo) CreateWithEvent(ctx context.Context, in NewOrder) (Order, error) {
	if r.jobs == nil {
		return Order{}, errors.New("orders: repo has no job inserter")
	}

	var created Order
	err := store.InTx(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		row, err := r.q.WithTx(tx).CreateOrder(ctx, sqlc.CreateOrderParams{
			OwnerID:     in.OwnerID,
			Reference:   in.Reference,
			AmountMinor: in.AmountMinor,
			Currency:    in.Currency,
		})
		if err != nil {
			return err
		}
		if _, err := r.jobs.InsertTx(ctx, tx, OrderCreatedArgs{OrderID: row.ID}, nil); err != nil {
			return fmt.Errorf("enqueue %s: %w", KindOrderCreated, err)
		}
		created = toOrder(row)
		return nil
	})
	if err != nil {
		if constraint, ok := store.UniqueViolation(err); ok && constraint == referenceConstraint {
			return Order{}, ErrReferenceTaken
		}
		return Order{}, fmt.Errorf("orders: create: %w", err)
	}
	return created, nil
}

// Get returns one order, or ErrNotFound.
func (r *Repo) Get(ctx context.Context, id int64) (Order, error) {
	row, err := r.q.GetOrder(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Order{}, ErrNotFound
	}
	if err != nil {
		return Order{}, fmt.Errorf("orders: get: %w", err)
	}
	return toOrder(row), nil
}

// ListByOwner returns up to limit orders of one owner with id < before, newest first.
func (r *Repo) ListByOwner(ctx context.Context, owner uuid.UUID, before int64, limit int) ([]Order, error) {
	rows, err := r.q.ListOrdersByOwner(ctx, sqlc.ListOrdersByOwnerParams{
		OwnerID:  owner,
		Cursor:   before,
		PageSize: int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("orders: list by owner: %w", err)
	}
	return toOrders(rows), nil
}

// ListAll returns up to limit orders with id < before, newest first.
func (r *Repo) ListAll(ctx context.Context, before int64, limit int) ([]Order, error) {
	rows, err := r.q.ListAllOrders(ctx, sqlc.ListAllOrdersParams{
		Cursor:   before,
		PageSize: int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("orders: list all: %w", err)
	}
	return toOrders(rows), nil
}

func toOrder(row sqlc.Order) Order {
	return Order{
		ID:          row.ID,
		OwnerID:     row.OwnerID,
		Reference:   row.Reference,
		AmountMinor: row.AmountMinor,
		Currency:    row.Currency,
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
	}
}

func toOrders(rows []sqlc.Order) []Order {
	out := make([]Order, 0, len(rows))
	for _, row := range rows {
		out = append(out, toOrder(row))
	}
	return out
}
