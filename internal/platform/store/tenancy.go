package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrInvalidTenantID is returned by InTenantTx and InTenantTxOptions when the
// caller passes uuid.Nil as the tenant identifier.
var ErrInvalidTenantID = errors.New("store: tenant ID is required")

// InTenantTx runs fn inside a transaction after binding the session variable
// "app.tenant_id" locally to the transaction via set_config(..., true).
//
// Because is_local is true (equivalent to SET LOCAL), PostgreSQL scopes the
// setting strictly to the current transaction and clears it automatically on
// COMMIT or ROLLBACK, making it safe to use behind PgBouncer in transaction
// pooling mode and with PostgreSQL Row-Level Security (RLS) policies that check:
//
//	NULLIF(current_setting('app.tenant_id', true), '')::uuid
func InTenantTx(ctx context.Context, db TxBeginner, tenantID uuid.UUID, fn func(ctx context.Context, tx pgx.Tx) error) error {
	if tenantID == uuid.Nil {
		return ErrInvalidTenantID
	}
	return pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id', $1, true)", tenantID.String()); err != nil {
			return fmt.Errorf("store: set tenant context: %w", err)
		}
		return fn(ctx, tx)
	})
}

// InTenantTxOptions is InTenantTx with explicit transaction options such as isolation level.
func InTenantTxOptions(ctx context.Context, db TxBeginner, tenantID uuid.UUID, opts pgx.TxOptions, fn func(ctx context.Context, tx pgx.Tx) error) error {
	if tenantID == uuid.Nil {
		return ErrInvalidTenantID
	}
	return pgx.BeginTxFunc(ctx, db, opts, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id', $1, true)", tenantID.String()); err != nil {
			return fmt.Errorf("store: set tenant context: %w", err)
		}
		return fn(ctx, tx)
	})
}
