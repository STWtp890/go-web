package postgres

import (
	"context"
	"errors"

	"document-service/internal/domain"

	"github.com/jackc/pgx/v5"
)

// The Outbox is append-only. There is deliberately no update and no delete
// statement for document_service.document_events anywhere in this service, and
// the database rejects both with a trigger.

const nextEventSequenceSQL = `
SELECT nextval(pg_get_serial_sequence('document_service.document_events', 'sequence'))`

// NextEventSequence reserves the cursor value of the next Outbox row. It is taken
// before the payload is rendered so the row and the sequence inside its payload
// always agree; a rolled back transaction burns the value, which is harmless for a
// cursor that only has to be monotonic.
func (store *Store) NextEventSequence(ctx context.Context, tx pgx.Tx) (int64, error) {
	var sequence int64
	if err := tx.QueryRow(ctx, nextEventSequenceSQL).Scan(&sequence); err != nil {
		return 0, mapError(err)
	}
	return sequence, nil
}

const insertEventSQL = `
INSERT INTO document_service.document_events (
    sequence, event_id, document_id, event_kind, aggregate_revision, dedupe_key, payload, occurred_at, created_at
) VALUES ($1, $2::text::uuid, $3::text::uuid, $4, $5, $6, $7, $8, $9)`

// EventSink appends one Outbox row inside the caller's transaction.
//
// It is an interface for the same reason AuditSink is one: a test has to be able
// to prove that a failing Outbox write rolls the whole business transaction back.
// Production always uses DefaultEventSink, which is the statement below and
// nothing else.
type EventSink interface {
	Append(ctx context.Context, tx pgx.Tx, event domain.Event, dedupeKey string) error
}

// DefaultEventSink is the production Outbox writer.
type DefaultEventSink struct{}

// Append writes one Outbox row.
func (DefaultEventSink) Append(ctx context.Context, tx pgx.Tx, event domain.Event, dedupeKey string) error {
	if event.Sequence <= 0 {
		return errors.New("document-service postgres: an Outbox event needs a reserved sequence")
	}
	_, err := tx.Exec(ctx, insertEventSQL,
		event.Sequence, event.EventID, event.DocumentID, event.EventKind,
		event.AggregateRevision, dedupeKey, event.Payload, event.OccurredAt, event.OccurredAt,
	)
	return mapError(err)
}

// AppendEvent writes one Outbox row in the caller's transaction through the
// store's sink.
func (store *Store) AppendEvent(ctx context.Context, tx pgx.Tx, event domain.Event, dedupeKey string) error {
	if store == nil || store.events == nil {
		return errors.New("document-service postgres: Outbox sink is not configured")
	}
	return store.events.Append(ctx, tx, event, dedupeKey)
}

const readEventsSQL = `
SELECT sequence, event_id::text, document_id::text, event_kind, aggregate_revision, payload, occurred_at
FROM document_service.document_events
WHERE sequence > $1
ORDER BY sequence
LIMIT $2`

// ReadEvents reads one batch of Outbox rows after the given cursor. The read runs
// at REPEATABLE READ so the batch is one snapshot even though it is the consumer
// that owns the cursor.
func (store *Store) ReadEvents(ctx context.Context, afterSequence int64, limit int) ([]domain.Event, error) {
	if limit <= 0 {
		limit = 100
	}
	events := make([]domain.Event, 0, limit)
	err := store.InConsistentRead(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, readEventsSQL, afterSequence, limit)
		if err != nil {
			return mapError(err)
		}
		defer rows.Close()
		for rows.Next() {
			var event domain.Event
			if err := rows.Scan(
				&event.Sequence, &event.EventID, &event.DocumentID, &event.EventKind,
				&event.AggregateRevision, &event.Payload, &event.OccurredAt,
			); err != nil {
				return mapError(err)
			}
			events = append(events, event)
		}
		return mapError(rows.Err())
	})
	if err != nil {
		return nil, err
	}
	return events, nil
}

const maxEventSequenceSQL = `SELECT COALESCE(MAX(sequence), 0) FROM document_service.document_events`

// MaxEventSequence returns the highest committed sequence, or 0 when the Outbox
// is empty. A cursor beyond this value can never be served, which is what makes a
// cursor that is ahead of the stream distinguishable from a drained one.
func (store *Store) MaxEventSequence(ctx context.Context) (int64, error) {
	var sequence int64
	if err := store.pool.QueryRow(ctx, maxEventSequenceSQL).Scan(&sequence); err != nil {
		return 0, mapError(err)
	}
	return sequence, nil
}
