package application

// This file rebuilds the derived index from local data only. There is no source
// client anywhere in this module: a rebuild reads the applied-event ledger and
// the stored rows this service already owns, so it cannot depend on py-agent
// being reachable, and it cannot ask py-agent to resend anything.
//
// The derived state is (status, revision) per record plus the search index
// entries those rows carry. Rebuilding therefore re-derives the lifecycle state
// from the ledger's newest event per record and rewrites each row, which
// re-materialises its full-text and trigram index entries.

import (
	"context"

	qqsearchv1 "packages/gen/qqsearch/v1"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	// rebuildRunOpen records the attempt before any work happens, so a crash
	// during a rebuild leaves a visible 'running' row instead of nothing.
	rebuildRunOpen = `
INSERT INTO qq_search.rebuild_runs(run_id, state)
VALUES (gen_random_uuid(), 'running')
RETURNING run_id::text`

	rebuildRunFinish = `
UPDATE qq_search.rebuild_runs
   SET state = $2, messages_rebuilt = $3, files_rebuilt = $4, records_failed = $5,
       last_error = $6, completed_at = clock_timestamp()
 WHERE run_id = $1::uuid`

	// rebuildMessages re-derives every message row from the newest applied event
	// for that record. A recall at the newest revision always wins, so a rebuild
	// can never resurrect a recalled message.
	//
	// The revision guard is what makes that true while the rebuild is running.
	// The statement's snapshot of the ledger is taken when it starts, so a recall
	// that commits while this statement waits on the row lock is invisible to
	// `latest`; without the guard the rebuild would then write the older revision
	// over the newer row and resurrect the record. A row whose stored revision is
	// newer than the snapshot is left to the writer that already advanced it.
	rebuildMessages = `
WITH latest AS (
    SELECT DISTINCT ON (record_kind, record_id)
           record_kind, record_id, record_revision, event_kind
      FROM qq_search.qq_applied_events
     ORDER BY record_kind, record_id, record_revision DESC
)
UPDATE qq_search.qq_messages AS m
   SET status = CASE WHEN latest.event_kind = 'message_recalled' THEN 'recalled' ELSE 'indexed' END,
       recalled_at = CASE WHEN latest.event_kind = 'message_recalled'
                          THEN COALESCE(m.recalled_at, clock_timestamp()) ELSE NULL END,
       record_revision = latest.record_revision,
       updated_at = clock_timestamp()
  FROM latest
 WHERE latest.record_kind = 'message'
   AND latest.record_id = m.record_id
   AND m.record_revision <= latest.record_revision`

	// rebuildFiles is the file model's own statement, with its own table, its own
	// recall kind and the same revision guard.
	rebuildFiles = `
WITH latest AS (
    SELECT DISTINCT ON (record_kind, record_id)
           record_kind, record_id, record_revision, event_kind
      FROM qq_search.qq_applied_events
     ORDER BY record_kind, record_id, record_revision DESC
)
UPDATE qq_search.qq_files AS f
   SET status = CASE WHEN latest.event_kind = 'file_recalled' THEN 'recalled' ELSE 'indexed' END,
       recalled_at = CASE WHEN latest.event_kind = 'file_recalled'
                          THEN COALESCE(f.recalled_at, clock_timestamp()) ELSE NULL END,
       record_revision = latest.record_revision,
       updated_at = clock_timestamp()
  FROM latest
 WHERE latest.record_kind = 'file'
   AND latest.record_id = f.record_id
   AND f.record_revision <= latest.record_revision`

	// countOrphanMessages finds rows that no applied event can explain. They are
	// left untouched and reported as records_failed: the service cannot invent
	// the content of a record it never received an event for.
	countOrphanMessages = `
SELECT count(*)
  FROM qq_search.qq_messages m
 WHERE NOT EXISTS (
       SELECT 1 FROM qq_search.qq_applied_events e
        WHERE e.record_kind = 'message' AND e.record_id = m.record_id)`

	countOrphanFiles = `
SELECT count(*)
  FROM qq_search.qq_files f
 WHERE NOT EXISTS (
       SELECT 1 FROM qq_search.qq_applied_events e
        WHERE e.record_kind = 'file' AND e.record_id = f.record_id)`

	// countUnrestorableMessages finds ledger entries whose row is missing. The
	// event ledger carries no payload, so those records cannot be restored from
	// local data and are reported as failures rather than silently dropped.
	countUnrestorableMessages = `
SELECT count(*)
  FROM (SELECT DISTINCT record_id FROM qq_search.qq_applied_events WHERE record_kind = 'message') e
 WHERE NOT EXISTS (SELECT 1 FROM qq_search.qq_messages m WHERE m.record_id = e.record_id)`

	countUnrestorableFiles = `
SELECT count(*)
  FROM (SELECT DISTINCT record_id FROM qq_search.qq_applied_events WHERE record_kind = 'file') e
 WHERE NOT EXISTS (SELECT 1 FROM qq_search.qq_files f WHERE f.record_id = e.record_id)`
)

// rebuild replays the locally applied events into both indexes.
func (service *Service) rebuild(ctx context.Context, confirm bool) (*qqsearchv1.RebuildIndexResponse, error) {
	if !confirm {
		// A rebuild rewrites every row in both corpora; it stays behind an
		// explicit confirmation instead of being one mistyped call away.
		return nil, status.Error(codes.FailedPrecondition, "qq-search: rebuild requires confirm=true")
	}
	if err := service.requirePool(); err != nil {
		return nil, err
	}
	pool := service.pool.Pgx()

	var runID string
	if err := pool.QueryRow(ctx, rebuildRunOpen).Scan(&runID); err != nil {
		return nil, internalError("open rebuild run: %v", err)
	}

	response, rebuildErr := service.runRebuild(ctx)
	if rebuildErr != nil {
		_, _ = pool.Exec(ctx, rebuildRunFinish, runID, "failed", 0, 0, 0, rebuildErr.Error())
		return nil, rebuildErr
	}
	if _, err := pool.Exec(ctx, rebuildRunFinish, runID, "succeeded",
		response.GetMessagesRebuilt(), response.GetFilesRebuilt(), response.GetRecordsFailed(), ""); err != nil {
		return nil, internalError("finish rebuild run: %v", err)
	}
	return response, nil
}

// runRebuild performs the rebuild in one transaction: either both corpora are
// re-derived or neither is.
func (service *Service) runRebuild(ctx context.Context) (*qqsearchv1.RebuildIndexResponse, error) {
	pool := service.pool.Pgx()
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Unavailable, "qq-search: begin rebuild: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	messages, err := rebuiltRows(ctx, tx, rebuildMessages)
	if err != nil {
		return nil, internalError("rebuild messages: %v", err)
	}
	files, err := rebuiltRows(ctx, tx, rebuildFiles)
	if err != nil {
		return nil, internalError("rebuild files: %v", err)
	}
	failed, err := countRows(ctx, tx, countOrphanMessages)
	if err != nil {
		return nil, internalError("count orphan messages: %v", err)
	}
	moreFailed, err := countRows(ctx, tx, countOrphanFiles)
	if err != nil {
		return nil, internalError("count orphan files: %v", err)
	}
	failed += moreFailed
	moreFailed, err = countRows(ctx, tx, countUnrestorableMessages)
	if err != nil {
		return nil, internalError("count unrestorable messages: %v", err)
	}
	failed += moreFailed
	moreFailed, err = countRows(ctx, tx, countUnrestorableFiles)
	if err != nil {
		return nil, internalError("count unrestorable files: %v", err)
	}
	failed += moreFailed

	if err := tx.Commit(ctx); err != nil {
		return nil, status.Errorf(codes.Unavailable, "qq-search: commit rebuild: %v", err)
	}
	return &qqsearchv1.RebuildIndexResponse{
		MessagesRebuilt: clampInt32(messages),
		FilesRebuilt:    clampInt32(files),
		RecordsFailed:   clampInt32(failed),
	}, nil
}

// rebuiltRows runs one corpus's rebuild statement and reports how many rows it
// re-derived.
func rebuiltRows(ctx context.Context, tx pgx.Tx, statement string) (int64, error) {
	tag, err := tx.Exec(ctx, statement)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// countRows runs one counting statement.
func countRows(ctx context.Context, tx pgx.Tx, statement string) (int64, error) {
	var count int64
	if err := tx.QueryRow(ctx, statement).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

// clampInt32 keeps a count representable on the wire. The contract's counters
// are int32 while the tables count in int64.
func clampInt32(value int64) int32 {
	const maxInt32 = int64(1)<<31 - 1
	switch {
	case value < 0:
		return 0
	case value > maxInt32:
		return int32(maxInt32)
	default:
		return int32(value)
	}
}
