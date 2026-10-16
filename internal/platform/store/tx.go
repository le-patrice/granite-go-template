package store

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// TxBeginner is satisfied by *pgxpool.Pool.
type TxBeginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
	BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error)
}

// InTx runs fn inside a transaction. It commits when fn returns nil and rolls
// back when fn returns an error or panics. Use it to write a business change
// and enqueue its job (River InsertTx) in one transaction.
func InTx(ctx context.Context, db TxBeginner, fn func(ctx context.Context, tx pgx.Tx) error) error {
	return pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		return fn(ctx, tx)
	})
}

// InTxOptions is InTx with explicit transaction options such as isolation level.
func InTxOptions(ctx context.Context, db TxBeginner, opts pgx.TxOptions, fn func(ctx context.Context, tx pgx.Tx) error) error {
	return pgx.BeginTxFunc(ctx, db, opts, func(tx pgx.Tx) error {
		return fn(ctx, tx)
	})
}
