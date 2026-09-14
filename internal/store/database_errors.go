package store

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrNotFound          = errors.New("not found")
	ErrForbidden         = errors.New("forbidden")
	ErrConflict          = errors.New("conflict")
	ErrReplay            = errors.New("replayed request")
	ErrInvalid           = errors.New("invalid input")
	ErrSecretUnavailable = errors.New("protected enrollment credential unavailable")
	ErrAuditUnavailable  = errors.New("audit unavailable")
)

func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if isUniqueViolation(err) {
		return fmt.Errorf("%w: duplicate value", ErrConflict)
	}
	return err
}

func isUniqueViolation(err error) bool {
	var pgError *pgconn.PgError
	return errors.As(err, &pgError) && pgError.Code == "23505"
}

func isForeignKeyViolation(err error) bool {
	var pgError *pgconn.PgError
	return errors.As(err, &pgError) && pgError.Code == "23503"
}
