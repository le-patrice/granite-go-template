package store

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// SQLSTATE codes used by the helpers below.
const (
	sqlStateForeignKeyViolation = "23503"
	sqlStateUniqueViolation     = "23505"
)

// UniqueViolation reports whether err is a unique-constraint violation and
// returns the violated constraint name. Repositories use it to translate
// driver errors into domain errors.
func UniqueViolation(err error) (constraint string, ok bool) {
	return violation(err, sqlStateUniqueViolation)
}

// ForeignKeyViolation reports whether err is a foreign-key violation and
// returns the violated constraint name.
func ForeignKeyViolation(err error) (constraint string, ok bool) {
	return violation(err, sqlStateForeignKeyViolation)
}

func violation(err error, code string) (string, bool) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == code {
		return pgErr.ConstraintName, true
	}
	return "", false
}
