package orders

import (
	"context"
	"errors"
	"testing"

	"github.com/riverqueue/river"
)

type stubGetter struct {
	order Order
	err   error
}

func (s stubGetter) Get(context.Context, int64) (Order, error) { return s.order, s.err }

type recordingNotifier struct {
	calls []Order
	err   error
}

func (n *recordingNotifier) OrderCreated(_ context.Context, order Order) error {
	n.calls = append(n.calls, order)
	return n.err
}

func job(orderID int64) *river.Job[OrderCreatedArgs] {
	return &river.Job[OrderCreatedArgs]{Args: OrderCreatedArgs{OrderID: orderID}}
}

func TestOrderCreatedArgsKind(t *testing.T) {
	if got := (OrderCreatedArgs{}).Kind(); got != "order.created" {
		t.Errorf("Kind = %q, want order.created", got)
	}
}

func TestWorkNotifiesOnceForTheOrder(t *testing.T) {
	notifier := &recordingNotifier{}
	worker := NewOrderCreatedWorker(stubGetter{order: Order{ID: 7, Reference: "r"}}, notifier)

	if err := worker.Work(context.Background(), job(7)); err != nil {
		t.Fatalf("Work: %v", err)
	}
	if len(notifier.calls) != 1 || notifier.calls[0].ID != 7 {
		t.Errorf("notifier calls = %+v, want one call for order 7", notifier.calls)
	}
}

func TestWorkCancelsWhenTheOrderIsGone(t *testing.T) {
	notifier := &recordingNotifier{}
	worker := NewOrderCreatedWorker(stubGetter{err: ErrNotFound}, notifier)

	err := worker.Work(context.Background(), job(7))
	if err == nil || !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want a cancellation wrapping ErrNotFound", err)
	}
	if len(notifier.calls) != 0 {
		t.Error("notifier ran for a missing order")
	}
}

func TestWorkReturnsErrorsSoRiverRetries(t *testing.T) {
	boom := errors.New("boom")

	loadFails := NewOrderCreatedWorker(stubGetter{err: boom}, &recordingNotifier{})
	if err := loadFails.Work(context.Background(), job(1)); !errors.Is(err, boom) {
		t.Errorf("load failure err = %v, want boom", err)
	}

	notifyFails := NewOrderCreatedWorker(stubGetter{order: Order{ID: 1}}, &recordingNotifier{err: boom})
	if err := notifyFails.Work(context.Background(), job(1)); !errors.Is(err, boom) {
		t.Errorf("notify failure err = %v, want boom", err)
	}
}
