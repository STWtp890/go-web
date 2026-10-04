// Package postgres owns the document service's SQL.
//
// One schema, one write account, every statement schema-qualified. The service
// never writes another service's tables, so the statements here name
// document_service explicitly even though the pool pins search_path; a role
// whose search_path differs must not be able to change what a statement means.
//
// This package translates SQL state into the domain sentinel errors declared in
// internal/domain. It contains no business rule: which revision moves, who may
// mutate a document and what a resolution means are decided in the application
// layer.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"document-service/internal/domain"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the persistence component of the document service.
type Store struct {
	pool   *pgxpool.Pool
	audit  AuditSink
	events EventSink
}

// NewStore builds the persistence component from an open pool.
func NewStore(pool *Pool) (*Store, error) {
	if pool == nil || pool.Pgx() == nil {
		return nil, errors.New("document-service postgres: pool is required")
	}
	return &Store{pool: pool.Pgx(), audit: DefaultAuditSink{}, events: DefaultEventSink{}}, nil
}

// WithAuditSink returns a copy of the store that appends audit rows through the
// given sink. It exists so a test can prove that a failing audit insert rolls the
// business mutation back; production always uses the default sink.
func (store *Store) WithAuditSink(sink AuditSink) *Store {
	clone := *store
	if sink != nil {
		clone.audit = sink
	}
	return &clone
}

// WithEventSink returns a copy of the store that appends Outbox rows through the
// given sink. It exists so a test can prove that a failing event insert rolls the
// business mutation back - including the version and the access policy that were
// written before it; production always uses the default sink.
func (store *Store) WithEventSink(sink EventSink) *Store {
	clone := *store
	if sink != nil {
		clone.events = sink
	}
	return &clone
}

// AuditSink returns the audit sink in use.
func (store *Store) AuditSink() AuditSink {
	if store == nil {
		return nil
	}
	return store.audit
}

// TxFunc is one unit of work. The transaction is committed when it returns nil
// and rolled back otherwise, so a partially applied command can never commit.
type TxFunc func(ctx context.Context, tx pgx.Tx) error

// InTransaction runs a read-write unit of work in one transaction.
func (store *Store) InTransaction(ctx context.Context, fn TxFunc) error {
	return store.runTx(ctx, pgx.TxOptions{}, fn)
}

// InReadOnly runs a read-only unit of work. The access mode is a guard rail: a
// read path that accidentally writes fails instead of mutating data.
func (store *Store) InReadOnly(ctx context.Context, fn TxFunc) error {
	return store.runTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly}, fn)
}

// InConsistentRead runs a unit of work at REPEATABLE READ, which gives every
// statement in it the same snapshot. The resolver needs this: a concurrent rebind
// must not let one answer mix the old binding with the new membership.
func (store *Store) InConsistentRead(ctx context.Context, fn TxFunc) error {
	return store.runTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, fn)
}

func (store *Store) runTx(ctx context.Context, options pgx.TxOptions, fn TxFunc) error {
	if store == nil || store.pool == nil {
		return fmt.Errorf("%w: database pool is not initialized", domain.ErrUnavailable)
	}
	if fn == nil {
		return errors.New("document-service postgres: transaction callback is nil")
	}
	tx, err := store.pool.BeginTx(ctx, options)
	if err != nil {
		return mapError(err)
	}
	defer func() {
		// A rollback after a successful commit reports ErrTxClosed, which is the
		// expected outcome of this deferred call and must not mask the result.
		_ = tx.Rollback(ctx)
	}()
	if err := fn(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return mapError(err)
	}
	return nil
}

// mapError converts a driver error into the domain vocabulary the use cases rely
// on. Anything it does not recognize is passed through unchanged so a defect is
// visible instead of being disguised as a caller error.
func mapError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, domain.ErrNotFound),
		errors.Is(err, domain.ErrForbidden),
		errors.Is(err, domain.ErrPrecondition),
		errors.Is(err, domain.ErrInvalidInput),
		errors.Is(err, domain.ErrAlreadyExists),
		errors.Is(err, domain.ErrUnavailable),
		errors.Is(err, domain.ErrInvariant):
		return err
	case errors.Is(err, pgx.ErrNoRows):
		return domain.ErrNotFound
	}

	var pgError *pgconn.PgError
	if errors.As(err, &pgError) {
		return mapPgError(pgError)
	}
	if isUnavailable(err) {
		return fmt.Errorf("%w: %v", domain.ErrUnavailable, err)
	}
	return err
}

func mapPgError(pgError *pgconn.PgError) error {
	switch pgError.Code {
	case "23505": // unique_violation
		return fmt.Errorf("%w: %s", domain.ErrAlreadyExists, strings.TrimSpace(pgError.ConstraintName))
	case "23503": // foreign_key_violation
		return fmt.Errorf("%w: %s", domain.ErrNotFound, strings.TrimSpace(pgError.ConstraintName))
	case "23514", // check_violation
		"22001", // string_data_right_truncation
		"22P02": // invalid_text_representation
		return fmt.Errorf("%w: %s", domain.ErrInvalidInput, strings.TrimSpace(pgError.Message))
	case "40001", // serialization_failure
		"40P01": // deadlock_detected
		return fmt.Errorf("%w: %s", domain.ErrUnavailable, strings.TrimSpace(pgError.Message))
	case "08000", "08003", "08006", // connection_exception family
		"53300",                   // too_many_connections
		"57P01", "57P02", "57P03": // admin shutdown, crash shutdown, cannot connect now
		return fmt.Errorf("%w: %s", domain.ErrUnavailable, strings.TrimSpace(pgError.Message))
	default:
		// A raised exception from one of our own triggers carries the business
		// meaning in its message; it is a precondition failure of the write, not a
		// transport failure.
		if pgError.Code == "P0001" {
			return fmt.Errorf("%w: %s", domain.ErrPrecondition, strings.TrimSpace(pgError.Message))
		}
		return fmt.Errorf("document-service postgres: %s (%s)", strings.TrimSpace(pgError.Message), pgError.Code)
	}
}

func isUnavailable(err error) bool {
	var connectError *pgconn.ConnectError
	if errors.As(err, &connectError) {
		return true
	}
	var netError net.Error
	if errors.As(err, &netError) {
		return true
	}
	if pgconn.SafeToRetry(err) {
		return true
	}
	return errors.Is(err, context.DeadlineExceeded)
}
