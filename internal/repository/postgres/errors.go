package postgres

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// pgerrUniqueViolation is Postgres' SQLSTATE for a unique-constraint violation.
const pgerrUniqueViolation = "23505"

// uniqueViolation reports whether err is a unique-constraint violation and, if
// so, which constraint raised it. A violation is a client-visible conflict, not
// an internal failure, so repositories translate it instead of letting a raw
// driver error — constraint name, SQLSTATE and all — reach the caller.
func uniqueViolation(err error) (constraint string, ok bool) {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != pgerrUniqueViolation {
		return "", false
	}
	return pgErr.ConstraintName, true
}
