package orders

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestNewOrderValidate(t *testing.T) {
	owner := uuid.New()
	tests := []struct {
		name      string
		input     NewOrder
		wantField string
	}{
		{"valid", NewOrder{OwnerID: owner, Reference: "ref-1", AmountMinor: 100, Currency: "USD"}, ""},
		{"normalizes reference and currency", NewOrder{OwnerID: owner, Reference: "  ref-1 ", AmountMinor: 100, Currency: " usd "}, ""},
		{"empty reference", NewOrder{Reference: "", AmountMinor: 100, Currency: "USD"}, "reference"},
		{"reference too long", NewOrder{Reference: strings.Repeat("r", 65), AmountMinor: 100, Currency: "USD"}, "reference"},
		{"zero amount", NewOrder{Reference: "r", AmountMinor: 0, Currency: "USD"}, "amount_minor"},
		{"negative amount", NewOrder{Reference: "r", AmountMinor: -5, Currency: "USD"}, "amount_minor"},
		{"short currency", NewOrder{Reference: "r", AmountMinor: 1, Currency: "US"}, "currency"},
		{"digits in currency", NewOrder{Reference: "r", AmountMinor: 1, Currency: "U5D"}, "currency"},
		{"long currency", NewOrder{Reference: "r", AmountMinor: 1, Currency: "USDX"}, "currency"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.input.Validate()
			if tt.wantField == "" {
				if err != nil {
					t.Fatalf("Validate: %v", err)
				}
				if got.Reference != "ref-1" && tt.name != "valid" {
					t.Errorf("Reference = %q, want trimmed", got.Reference)
				}
				if got.Currency != "USD" {
					t.Errorf("Currency = %q, want USD", got.Currency)
				}
				return
			}
			var validation *ValidationError
			if !errors.As(err, &validation) || validation.Field != tt.wantField {
				t.Errorf("err = %v, want a validation error for %s", err, tt.wantField)
			}
		})
	}
}

func TestCursorRoundTripAndRejection(t *testing.T) {
	for _, id := range []int64{1, 42, 9_000_000_000} {
		got, err := DecodeCursor(EncodeCursor(id))
		if err != nil || got != id {
			t.Errorf("round trip of %d = %d, %v", id, got, err)
		}
	}
	for _, bad := range []string{"", "!!!", "YWJj", EncodeCursor(0), "LTU"} {
		if _, err := DecodeCursor(bad); !errors.Is(err, ErrInvalidCursor) {
			t.Errorf("DecodeCursor(%q) err = %v, want ErrInvalidCursor", bad, err)
		}
	}
}

func TestVisibleTo(t *testing.T) {
	owner, other := uuid.New(), uuid.New()
	order := Order{OwnerID: owner}

	tests := []struct {
		name  string
		actor Actor
		want  bool
	}{
		{"owner", Actor{UserID: owner}, true},
		{"another member", Actor{UserID: other}, false},
		{"admin", Actor{UserID: other, Admin: true}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := order.VisibleTo(tt.actor); got != tt.want {
				t.Errorf("VisibleTo = %v, want %v", got, tt.want)
			}
		})
	}
}
