// Package orders is the reference feature: owner-scoped orders with a
// transactional outbox. It is an example and can be removed with
// "make strip-examples CONFIRM=yes".
package orders

import (
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	// DefaultPageSize is used when a list request gives no limit.
	DefaultPageSize = 20
	// MaxPageSize caps the limit of a list request.
	MaxPageSize = 100

	maxReferenceLength = 64
)

// Domain errors. Their messages are safe to show to clients.
var (
	ErrNotFound       = errors.New("order not found")
	ErrReferenceTaken = errors.New("an order with this reference already exists")
	ErrInvalidCursor  = errors.New("cursor is not valid")
)

// ValidationError reports a rejected field.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return e.Field + ": " + e.Message
}

// Order is a purchase owned by one user. Money is integer minor units.
type Order struct {
	ID          int64
	OwnerID     uuid.UUID
	Reference   string
	AmountMinor int64
	Currency    string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Actor is the caller as the orders rules see it.
type Actor struct {
	UserID uuid.UUID
	Admin  bool
}

// VisibleTo reports whether actor may read the order: admins read all, members
// read only their own.
func (o Order) VisibleTo(actor Actor) bool {
	return actor.Admin || o.OwnerID == actor.UserID
}

// NewOrder is the input to order creation.
type NewOrder struct {
	OwnerID     uuid.UUID
	Reference   string
	AmountMinor int64
	Currency    string
}

// Validate normalizes the input (trimmed reference, upper-case currency) and
// checks every field.
func (n NewOrder) Validate() (NewOrder, error) {
	n.Reference = strings.TrimSpace(n.Reference)
	n.Currency = strings.ToUpper(strings.TrimSpace(n.Currency))

	if n.Reference == "" || len(n.Reference) > maxReferenceLength {
		return NewOrder{}, &ValidationError{Field: "reference", Message: "must be between 1 and 64 characters"}
	}
	if n.AmountMinor <= 0 {
		return NewOrder{}, &ValidationError{Field: "amount_minor", Message: "must be a positive integer of minor units"}
	}
	if len(n.Currency) != 3 || strings.Trim(n.Currency, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "" {
		return NewOrder{}, &ValidationError{Field: "currency", Message: "must be a three-letter code such as USD"}
	}
	return n, nil
}

// Page is one page of a keyset-paginated list. NextCursor is empty on the last page.
type Page struct {
	Items      []Order
	NextCursor string
}

// EncodeCursor turns the last seen order ID into an opaque cursor.
func EncodeCursor(id int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(id, 10)))
}

// DecodeCursor reverses EncodeCursor.
func DecodeCursor(cursor string) (int64, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, ErrInvalidCursor
	}
	id, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil || id < 1 {
		return 0, ErrInvalidCursor
	}
	return id, nil
}
