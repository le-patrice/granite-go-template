package orders

import (
	"context"
	"fmt"
	"math"

	"github.com/google/uuid"
)

// Repository is the persistence the service needs. repo.go implements it.
type Repository interface {
	// CreateWithEvent stores the order and enqueues its order.created job in
	// one transaction: both exist or neither does.
	CreateWithEvent(ctx context.Context, order NewOrder) (Order, error)
	Get(ctx context.Context, id int64) (Order, error)
	ListByOwner(ctx context.Context, owner uuid.UUID, before int64, limit int) ([]Order, error)
	ListAll(ctx context.Context, before int64, limit int) ([]Order, error)
}

// Service holds the order use-cases and enforces ownership.
type Service struct {
	repo Repository
}

// NewService returns a Service over repo.
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// Create places an order owned by actor. The owner is always the caller.
func (s *Service) Create(ctx context.Context, actor Actor, in NewOrder) (Order, error) {
	in.OwnerID = actor.UserID
	valid, err := in.Validate()
	if err != nil {
		return Order{}, err
	}
	order, err := s.repo.CreateWithEvent(ctx, valid)
	if err != nil {
		return Order{}, err
	}
	return order, nil
}

// Get returns an order the actor may see. An order that exists but belongs to
// someone else is reported as ErrNotFound, so its existence is not revealed.
func (s *Service) Get(ctx context.Context, actor Actor, id int64) (Order, error) {
	order, err := s.repo.Get(ctx, id)
	if err != nil {
		return Order{}, err
	}
	if !order.VisibleTo(actor) {
		return Order{}, ErrNotFound
	}
	return order, nil
}

// List returns one keyset page, newest first. Members see their own orders and
// admins see all. A limit outside 1..MaxPageSize is replaced by the default or
// the cap.
func (s *Service) List(ctx context.Context, actor Actor, cursor string, limit int) (Page, error) {
	before := int64(math.MaxInt64)
	if cursor != "" {
		id, err := DecodeCursor(cursor)
		if err != nil {
			return Page{}, err
		}
		before = id
	}
	size := pageSize(limit)

	var (
		rows []Order
		err  error
	)
	if actor.Admin {
		rows, err = s.repo.ListAll(ctx, before, size+1)
	} else {
		rows, err = s.repo.ListByOwner(ctx, actor.UserID, before, size+1)
	}
	if err != nil {
		return Page{}, fmt.Errorf("orders: list: %w", err)
	}

	page := Page{Items: rows}
	if len(rows) > size {
		page.Items = rows[:size]
		page.NextCursor = EncodeCursor(rows[size-1].ID)
	}
	return page, nil
}

func pageSize(limit int) int {
	switch {
	case limit < 1:
		return DefaultPageSize
	case limit > MaxPageSize:
		return MaxPageSize
	default:
		return limit
	}
}
