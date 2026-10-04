package application

// This file applies one qqsource.v1 event per transaction. The order is fixed:
//
//	lock the record -> duplicate event? -> revision fence -> ledger -> row -> cursor
//
// so a redelivered event is a no-op, a stale one is ignored, a rewound revision
// can never overwrite a newer one, and a recall can never be undone by an older
// event. Nothing here is shared between the message and file models: only the
// ledger, the cursor and the transaction are, and those carry no record shape.

import (
	"context"
	"errors"

	qqsearchv1 "packages/gen/qqsearch/v1"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// advisoryLockQuery serializes events that address the same record. The fence is
// a read-then-write decision, so without it two concurrent events could both
// read "nothing applied" and both try to apply.
const advisoryLockQuery = `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`

const (
	// ledgerEventLookup answers "has this exact event been applied?".
	ledgerEventLookup = `SELECT 1 FROM qq_search.qq_applied_events WHERE event_id = $1::uuid`

	// ledgerInsert records the applied event. The event id is the primary key and
	// (record_kind, record_id, record_revision) is unique, so the ledger itself
	// refuses to hold one revision of one record twice.
	ledgerInsert = `
INSERT INTO qq_search.qq_applied_events(event_id, sequence, record_kind, record_id, record_revision, event_kind)
VALUES ($1::uuid, $2, $3, $4, $5, $6)
ON CONFLICT (event_id) DO NOTHING`

	// ledgerHighWater is the newest revision ever applied for one record.
	ledgerHighWater = `
SELECT COALESCE(MAX(record_revision), 0)
  FROM qq_search.qq_applied_events
 WHERE record_kind = $1 AND record_id = $2`

	// consumerAdvance moves the cursor forward only: events may arrive out of
	// order, and a cursor that moved backwards would replay applied events. The
	// GREATEST runs inside the upsert so concurrent events for different records
	// cannot interleave a read and a write; the existing row is referenced by the
	// table's own name, the form PostgreSQL documents for ON CONFLICT DO UPDATE.
	consumerAdvance = `
INSERT INTO qq_search.consumer_state(stream, last_sequence)
VALUES ($1, $2)
ON CONFLICT (stream) DO UPDATE SET
    last_sequence = GREATEST(consumer_state.last_sequence, EXCLUDED.last_sequence),
    updated_at = clock_timestamp()`
)

// lockKey is the advisory-lock key of one record inside its own corpus.
func (event *sourceEvent) lockKey() string {
	return "qq-search:" + string(event.recordKind) + ":" + event.recordID
}

// applyEvent applies one qqsource.v1 event idempotently and order-tolerantly.
func (service *Service) applyEvent(ctx context.Context, request *qqsearchv1.IndexQQSourceEventRequest) (*qqsearchv1.IndexQQSourceEventResponse, error) {
	event, err := parseSourceEvent(request)
	if err != nil {
		return nil, err
	}
	pool := service.pool.Pgx()
	if pool == nil {
		return nil, internalError("database pool is not available")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "qq-search: begin transaction: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, advisoryLockQuery, event.lockKey()); err != nil {
		return nil, internalError("lock record %s: %v", event.recordID, err)
	}

	duplicate, err := ledgerHasEvent(ctx, tx, event.ledgerEventID)
	if err != nil {
		return nil, internalError("read applied-event ledger: %v", err)
	}
	if duplicate {
		return service.settle(ctx, tx, event, ReasonDuplicate)
	}

	appliedRevision, err := ledgerRevision(ctx, tx, event)
	if err != nil {
		return nil, internalError("read applied revision: %v", err)
	}

	decision, reason, err := service.fence(ctx, tx, event, appliedRevision)
	if err != nil {
		return nil, internalError("read applied record: %v", err)
	}
	switch decision {
	case fenceStaleRevision:
		// A lower revision than the one already applied is ignored. This is what
		// stops a late upsert from resurrecting a recalled record.
		return service.settle(ctx, tx, event, ReasonStaleRevision)
	case fenceDuplicateRevision:
		return service.settle(ctx, tx, event, ReasonDuplicateRevision)
	case fenceConflict:
		return nil, status.Errorf(codes.AlreadyExists,
			"qq-search: %s %s revision %d is already applied: %s",
			event.recordKind, event.recordID, event.revision, reason)
	}

	tag, err := tx.Exec(ctx, ledgerInsert,
		event.ledgerEventID, event.sequence, string(event.recordKind), event.recordID, event.revision, event.eventKind)
	if err != nil {
		// The unique index on (record_kind, record_id, record_revision) fired: a
		// different event already claimed this revision of this record.
		if isUniqueViolation(err) {
			return nil, status.Errorf(codes.AlreadyExists,
				"qq-search: %s %s revision %d is already applied by another event",
				event.recordKind, event.recordID, event.revision)
		}
		return nil, internalError("record applied event: %v", err)
	}
	if tag.RowsAffected() == 0 {
		// Raced by an identical event id: the ledger already holds it.
		return service.settle(ctx, tx, event, ReasonDuplicate)
	}

	if err := service.writeRecord(ctx, tx, event); err != nil {
		return nil, internalError("apply %s for %s %s: %v", event.eventKind, event.recordKind, event.recordID, err)
	}
	if _, err := tx.Exec(ctx, consumerAdvance, consumerStream, event.sequence); err != nil {
		return nil, internalError("advance consumer cursor: %v", err)
	}

	state, err := service.recordState(ctx, tx, event.recordKind, event.recordID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, status.Errorf(codes.Unavailable, "qq-search: commit event: %v", err)
	}
	return &qqsearchv1.IndexQQSourceEventResponse{
		Applied: true,
		Reason:  ReasonApplied,
		State:   state,
	}, nil
}

// writeRecord dispatches to the lifecycle transition of the event's own model.
func (service *Service) writeRecord(ctx context.Context, tx pgx.Tx, event *sourceEvent) error {
	switch event.eventKind {
	case EventMessageUpsert:
		return service.upsertMessage(ctx, tx, event)
	case EventMessageRecalled:
		return service.recallMessage(ctx, tx, event)
	case EventFileUpsert:
		return service.upsertFile(ctx, tx, event)
	case EventFileRecalled:
		return service.recallFile(ctx, tx, event)
	default:
		return invalidArgument("unknown kind %q", event.eventKind)
	}
}

// settle commits a decision that changed nothing and reports it with the record's
// current state. Not applying an event is a normal outcome, not a failure: the
// response carries applied=false and the reason.
func (service *Service) settle(ctx context.Context, tx pgx.Tx, event *sourceEvent, reason string) (*qqsearchv1.IndexQQSourceEventResponse, error) {
	state, err := service.recordState(ctx, tx, event.recordKind, event.recordID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, status.Errorf(codes.Unavailable, "qq-search: commit %s: %v", reason, err)
	}
	return &qqsearchv1.IndexQQSourceEventResponse{
		Applied: false,
		Reason:  reason,
		State:   state,
	}, nil
}

// fence compares the event with what is already applied for its record.
func (service *Service) fence(ctx context.Context, tx pgx.Tx, event *sourceEvent, appliedRevision int64) (fenceDecision, string, error) {
	switch event.recordKind {
	case RecordKindMessage:
		row, found, err := loadMessageRow(ctx, tx, event.recordID)
		if err != nil {
			return fenceApply, "", err
		}
		if !found {
			row = nil
		}
		decision := decideMessageFence(event, newestRevision(appliedRevision, messageRevision(row)), row)
		return decision, conflictReason(event, row != nil && !identityMatchesMessage(event, row)), nil
	case RecordKindFile:
		row, found, err := loadFileRow(ctx, tx, event.recordID)
		if err != nil {
			return fenceApply, "", err
		}
		if !found {
			row = nil
		}
		decision := decideFileFence(event, newestRevision(appliedRevision, fileRevision(row)), row)
		return decision, conflictReason(event, row != nil && !identityMatchesFile(event, row)), nil
	default:
		return fenceApply, "", invalidArgument("unknown record kind %q", event.recordKind)
	}
}

// newestRevision is the higher of the ledger's high-water mark and the stored
// row's revision. Both are consulted because they answer different questions:
// the ledger remembers every applied event, the row remembers what is currently
// visible. The fence must never accept an event older than either.
func newestRevision(ledgerRevision, rowRevision int64) int64 {
	if rowRevision > ledgerRevision {
		return rowRevision
	}
	return ledgerRevision
}

// messageRevision is the message model's own revision accessor; a nil row has no
// revision. Files have their own accessor rather than a shared interface, so the
// two models cannot be passed to each other's lifecycle by accident.
func messageRevision(row *messageRow) int64 {
	if row == nil {
		return 0
	}
	return row.revision
}

// fileRevision is the file model's own revision accessor.
func fileRevision(row *fileRow) int64 {
	if row == nil {
		return 0
	}
	return row.revision
}

// recordState reads one record's state inside the caller's transaction.
func (service *Service) recordState(ctx context.Context, q querier, kind RecordKind, recordID string) (*qqsearchv1.QQRecordState, error) {
	switch kind {
	case RecordKindMessage:
		state, found, err := loadMessageState(ctx, q, recordID)
		if err != nil {
			return nil, internalError("read message state: %v", err)
		}
		if !found {
			return nil, nil
		}
		return state, nil
	case RecordKindFile:
		state, found, err := loadFileState(ctx, q, recordID)
		if err != nil {
			return nil, internalError("read file state: %v", err)
		}
		if !found {
			return nil, nil
		}
		return state, nil
	default:
		return nil, internalError("unknown record kind %q", kind)
	}
}

// ledgerHasEvent reports whether the exact event id has already been applied.
func ledgerHasEvent(ctx context.Context, tx pgx.Tx, eventID string) (bool, error) {
	var one int
	err := tx.QueryRow(ctx, ledgerEventLookup, eventID).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// ledgerRevision is the newest revision applied for this record, or 0 when the
// record has never been seen.
func ledgerRevision(ctx context.Context, tx pgx.Tx, event *sourceEvent) (int64, error) {
	var revision int64
	if err := tx.QueryRow(ctx, ledgerHighWater, string(event.recordKind), event.recordID).Scan(&revision); err != nil {
		return 0, err
	}
	return revision, nil
}
