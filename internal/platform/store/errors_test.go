package store

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestViolationHelpers(t *testing.T) {
	unique := fmt.Errorf("insert user: %w", &pgconn.PgError{Code: "23505", ConstraintName: "users_email_lower_key"})
	foreign := fmt.Errorf("insert token: %w", &pgconn.PgError{Code: "23503", ConstraintName: "refresh_tokens_user_id_fkey"})
	other := &pgconn.PgError{Code: "23514", ConstraintName: "users_role_check"}

	tests := []struct {
		name           string
		fn             func(error) (string, bool)
		err            error
		wantConstraint string
		wantOK         bool
	}{
		{"unique match", UniqueViolation, unique, "users_email_lower_key", true},
		{"unique on foreign key", UniqueViolation, foreign, "", false},
		{"unique on check violation", UniqueViolation, other, "", false},
		{"unique on plain error", UniqueViolation, errors.New("boom"), "", false},
		{"unique on nil", UniqueViolation, nil, "", false},
		{"foreign key match", ForeignKeyViolation, foreign, "refresh_tokens_user_id_fkey", true},
		{"foreign key on unique", ForeignKeyViolation, unique, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			constraint, ok := tt.fn(tt.err)
			if constraint != tt.wantConstraint || ok != tt.wantOK {
				t.Errorf("got (%q, %v), want (%q, %v)", constraint, ok, tt.wantConstraint, tt.wantOK)
			}
		})
	}
}
