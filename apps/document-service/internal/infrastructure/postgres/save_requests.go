package postgres

import (
	"context"
	"errors"

	"document-service/internal/domain"

	"github.com/jackc/pgx/v5"
)

// This file owns the durable SaveDocument idempotency ledger.
//
// The ledger is the single source of truth for "has this request id already been
// committed for this document": documents.last_save_request_id could only
// remember the most recent save, so a retry that arrived after a later save was
// re-executed and overwrote the newer body. One row per committed
// (document_id, request_id) recognizes a retry whenever it arrives, and the row
// is written in the same transaction as the version it names.

const saveRequestColumns = `document_id::text, request_id, version_id::text, payload_fingerprint, created_at`

const selectSaveRequestSQL = `
SELECT ` + saveRequestColumns + `
FROM document_service.document_save_requests
WHERE document_id = $1::text::uuid AND request_id = $2`

// FindSaveRequest reads the ledger entry of one request id. The second result is
// false when the request id was never committed for this document, which is the
// normal case for a first attempt.
func (store *Store) FindSaveRequest(ctx context.Context, tx pgx.Tx, documentID, requestID string) (domain.SaveRequest, bool, error) {
	var entry domain.SaveRequest
	err := tx.QueryRow(ctx, selectSaveRequestSQL, documentID, requestID).Scan(
		&entry.DocumentID, &entry.RequestID, &entry.VersionID, &entry.PayloadFingerprint, &entry.CreatedAt,
	)
	// This row is read directly rather than through scanDocument, so a miss
	// arrives as the driver's ErrNoRows. Accept the mapped sentinel too: "no entry
	// yet" is a first attempt, never a failure.
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, domain.ErrNotFound) {
		return domain.SaveRequest{}, false, nil
	}
	if err != nil {
		return domain.SaveRequest{}, false, mapError(err)
	}
	return entry, true, nil
}

const insertSaveRequestSQL = `
INSERT INTO document_service.document_save_requests (
    document_id, request_id, version_id, payload_fingerprint, created_at
) VALUES ($1::text::uuid, $2, $3::text::uuid, $4, $5)`

// InsertSaveRequest records one committed request id together with the version it
// produced. It runs inside the save transaction, after the version row exists and
// under the document row lock, so the recorded version always exists and two
// concurrent attempts with the same request id cannot both pass the lookup. A
// duplicate that still reached the database surfaces as ErrAlreadyExists instead
// of silently overwriting the first entry.
func (store *Store) InsertSaveRequest(ctx context.Context, tx pgx.Tx, entry domain.SaveRequest) error {
	_, err := tx.Exec(ctx, insertSaveRequestSQL,
		entry.DocumentID, entry.RequestID, entry.VersionID, entry.PayloadFingerprint, entry.CreatedAt,
	)
	return mapError(err)
}
