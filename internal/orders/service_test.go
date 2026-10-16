package orders

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

type fakeRepo struct {
	orders    []Order
	nextID    int64
	lastLimit int
}

func (f *fakeRepo) CreateWithEvent(_ context.Context, in NewOrder) (Order, error) {
	for _, o := range f.orders {
		if o.OwnerID == in.OwnerID && o.Reference == in.Reference {
			return Order{}, ErrReferenceTaken
		}
	}
	f.nextID++
	order := Order{
		ID:          f.nextID,
		OwnerID:     in.OwnerID,
		Reference:   in.Reference,
		AmountMinor: in.AmountMinor,
		Currency:    in.Currency,
	}
	f.orders = append(f.orders, order)
	return order, nil
}

func (f *fakeRepo) Get(_ context.Context, id int64) (Order, error) {
	for _, o := range f.orders {
		if o.ID == id {
			return o, nil
		}
	}
	return Order{}, ErrNotFound
}

func (f *fakeRepo) ListByOwner(_ context.Context, owner uuid.UUID, before int64, limit int) ([]Order, error) {
	f.lastLimit = limit
	return f.collect(func(o Order) bool { return o.OwnerID == owner && o.ID < before }, limit), nil
}

func (f *fakeRepo) ListAll(_ context.Context, before int64, limit int) ([]Order, error) {
	f.lastLimit = limit
	return f.collect(func(o Order) bool { return o.ID < before }, limit), nil
}

func (f *fakeRepo) collect(match func(Order) bool, limit int) []Order {
	var out []Order
	for i := len(f.orders) - 1; i >= 0 && len(out) < limit; i-- {
		if match(f.orders[i]) {
			out = append(out, f.orders[i])
		}
	}
	return out
}

func newService() (*Service, *fakeRepo) {
	repo := &fakeRepo{}
	return NewService(repo), repo
}

func place(t *testing.T, svc *Service, actor Actor, reference string) Order {
	t.Helper()
	order, err := svc.Create(context.Background(), actor, NewOrder{Reference: reference, AmountMinor: 100, Currency: "usd"})
	if err != nil {
		t.Fatalf("Create(%s): %v", reference, err)
	}
	return order
}

func TestCreateAlwaysUsesTheCallerAsOwner(t *testing.T) {
	svc, _ := newService()
	caller := Actor{UserID: uuid.New()}

	order, err := svc.Create(context.Background(), caller, NewOrder{
		OwnerID:     uuid.New(),
		Reference:   " ref-1 ",
		AmountMinor: 500,
		Currency:    "eur",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if order.OwnerID != caller.UserID {
		t.Errorf("OwnerID = %s, want the caller %s", order.OwnerID, caller.UserID)
	}
	if order.Reference != "ref-1" || order.Currency != "EUR" {
		t.Errorf("order not normalized: %+v", order)
	}
}

func TestCreateRejectsInvalidAndDuplicateOrders(t *testing.T) {
	svc, _ := newService()
	caller := Actor{UserID: uuid.New()}
	place(t, svc, caller, "dup")

	var validation *ValidationError
	if _, err := svc.Create(context.Background(), caller, NewOrder{Reference: "x", AmountMinor: 0, Currency: "USD"}); !errors.As(err, &validation) {
		t.Errorf("zero amount err = %v, want a validation error", err)
	}
	if _, err := svc.Create(context.Background(), caller, NewOrder{Reference: "dup", AmountMinor: 1, Currency: "USD"}); !errors.Is(err, ErrReferenceTaken) {
		t.Errorf("duplicate err = %v, want ErrReferenceTaken", err)
	}
}

func TestGetEnforcesOwnership(t *testing.T) {
	svc, _ := newService()
	alice := Actor{UserID: uuid.New()}
	bob := Actor{UserID: uuid.New()}
	admin := Actor{UserID: uuid.New(), Admin: true}
	order := place(t, svc, alice, "a-1")

	tests := []struct {
		name    string
		actor   Actor
		id      int64
		wantErr error
	}{
		{"owner reads own", alice, order.ID, nil},
		{"admin reads any", admin, order.ID, nil},
		{"member cannot read another's order", bob, order.ID, ErrNotFound},
		{"missing order", alice, 999, ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.Get(context.Background(), tt.actor, tt.id)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestListScopesByRoleAndPaginates(t *testing.T) {
	svc, _ := newService()
	ctx := context.Background()
	alice := Actor{UserID: uuid.New()}
	bob := Actor{UserID: uuid.New()}
	admin := Actor{UserID: uuid.New(), Admin: true}
	for _, ref := range []string{"a1", "a2", "a3", "a4", "a5"} {
		place(t, svc, alice, ref)
	}
	for _, ref := range []string{"b1", "b2", "b3"} {
		place(t, svc, bob, ref)
	}

	first, err := svc.List(ctx, alice, "", 2)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(first.Items) != 2 || first.NextCursor == "" {
		t.Fatalf("first page = %d items, cursor %q; want 2 items and a cursor", len(first.Items), first.NextCursor)
	}
	if first.Items[0].ID <= first.Items[1].ID {
		t.Errorf("items are not newest first: %d, %d", first.Items[0].ID, first.Items[1].ID)
	}

	seen := map[int64]bool{}
	page := first
	for {
		for _, o := range page.Items {
			if o.OwnerID != alice.UserID {
				t.Fatalf("member saw another owner's order %d", o.ID)
			}
			if seen[o.ID] {
				t.Fatalf("order %d appeared on two pages", o.ID)
			}
			seen[o.ID] = true
		}
		if page.NextCursor == "" {
			break
		}
		if page, err = svc.List(ctx, alice, page.NextCursor, 2); err != nil {
			t.Fatalf("List next: %v", err)
		}
	}
	if len(seen) != 5 {
		t.Errorf("member paged through %d orders, want 5", len(seen))
	}

	all, err := svc.List(ctx, admin, "", 100)
	if err != nil {
		t.Fatalf("List as admin: %v", err)
	}
	if len(all.Items) != 8 || all.NextCursor != "" {
		t.Errorf("admin saw %d orders, cursor %q; want all 8 and no cursor", len(all.Items), all.NextCursor)
	}
}

func TestListLimitIsDefaultedAndCapped(t *testing.T) {
	svc, repo := newService()
	actor := Actor{UserID: uuid.New()}

	tests := []struct {
		name      string
		limit     int
		wantFetch int
	}{
		{"zero uses the default", 0, DefaultPageSize + 1},
		{"negative uses the default", -3, DefaultPageSize + 1},
		{"within range", 7, 8},
		{"at the cap", MaxPageSize, MaxPageSize + 1},
		{"above the cap is capped", 1000, MaxPageSize + 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := svc.List(context.Background(), actor, "", tt.limit); err != nil {
				t.Fatalf("List: %v", err)
			}
			if repo.lastLimit != tt.wantFetch {
				t.Errorf("repository asked for %d rows, want %d", repo.lastLimit, tt.wantFetch)
			}
		})
	}
}

func TestListRejectsAnInvalidCursor(t *testing.T) {
	svc, _ := newService()
	if _, err := svc.List(context.Background(), Actor{UserID: uuid.New()}, "not-a-cursor!", 10); !errors.Is(err, ErrInvalidCursor) {
		t.Errorf("err = %v, want ErrInvalidCursor", err)
	}
}
