package orders

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/riverqueue/river"
)

// KindOrderCreated is the River job kind enqueued with every new order.
const KindOrderCreated = "order.created"

// OrderCreatedArgs are the arguments of an order.created job.
type OrderCreatedArgs struct {
	OrderID int64 `json:"order_id"`
}

// Kind implements river.JobArgs.
func (OrderCreatedArgs) Kind() string { return KindOrderCreated }

// OrderGetter loads an order for the worker. *Repo implements it.
type OrderGetter interface {
	Get(ctx context.Context, id int64) (Order, error)
}

// Notifier announces a new order. Jobs run at least once, so implementations
// must be idempotent: repeating OrderCreated for the same Order.ID must have no
// further effect (for example by keying an external call on the order ID).
type Notifier interface {
	OrderCreated(ctx context.Context, order Order) error
}

// OrderCreatedWorker handles order.created jobs.
type OrderCreatedWorker struct {
	river.WorkerDefaults[OrderCreatedArgs]
	orders   OrderGetter
	notifier Notifier
}

// NewOrderCreatedWorker returns a worker over its two collaborators.
func NewOrderCreatedWorker(orders OrderGetter, notifier Notifier) *OrderCreatedWorker {
	return &OrderCreatedWorker{orders: orders, notifier: notifier}
}

// Work loads the order and notifies. A missing order cancels the job instead of
// retrying, since retrying cannot help.
func (w *OrderCreatedWorker) Work(ctx context.Context, job *river.Job[OrderCreatedArgs]) error {
	order, err := w.orders.Get(ctx, job.Args.OrderID)
	if errors.Is(err, ErrNotFound) {
		return river.JobCancel(fmt.Errorf("orders: order %d no longer exists: %w", job.Args.OrderID, err))
	}
	if err != nil {
		return fmt.Errorf("orders: load order %d: %w", job.Args.OrderID, err)
	}
	if err := w.notifier.OrderCreated(ctx, order); err != nil {
		return fmt.Errorf("orders: notify order %d: %w", order.ID, err)
	}
	return nil
}

// RegisterWorkers adds this feature's workers to the bundle.
func RegisterWorkers(workers *river.Workers, orders OrderGetter, notifier Notifier) {
	river.AddWorker(workers, NewOrderCreatedWorker(orders, notifier))
}

// LogNotifier is the example Notifier. Logging is naturally idempotent.
type LogNotifier struct {
	logger *slog.Logger
}

// NewLogNotifier returns a Notifier that logs each new order.
func NewLogNotifier(logger *slog.Logger) *LogNotifier {
	return &LogNotifier{logger: logger}
}

// OrderCreated logs the order.
func (n *LogNotifier) OrderCreated(ctx context.Context, order Order) error {
	n.logger.InfoContext(ctx, "order created",
		"order_id", order.ID,
		"amount_minor", order.AmountMinor,
		"currency", order.Currency,
	)
	return nil
}
