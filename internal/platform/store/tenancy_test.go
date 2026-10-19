package store

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestInTenantTxRejectsNilUUID(t *testing.T) {
	err := InTenantTx(context.Background(), nil, uuid.Nil, func(context.Context, pgx.Tx) error {
		t.Fatal("callback must not run when tenantID is uuid.Nil")
		return nil
	})
	if !errors.Is(err, ErrInvalidTenantID) {
		t.Fatalf("err = %v, want ErrInvalidTenantID", err)
	}

	err = InTenantTxOptions(context.Background(), nil, uuid.Nil, pgx.TxOptions{}, func(context.Context, pgx.Tx) error {
		t.Fatal("callback must not run when tenantID is uuid.Nil")
		return nil
	})
	if !errors.Is(err, ErrInvalidTenantID) {
		t.Fatalf("err = %v, want ErrInvalidTenantID", err)
	}
}
