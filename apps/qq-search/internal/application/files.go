package application

// This file owns the raw QQ *file* model: its row shape, its two lifecycle
// transitions (upsert, recall) and its retrieval statement. It is the mirror
// image of messages.go and shares none of it. The two corpora have separate
// tables and separate index collections, so a file search can only ever read
// qq_search.qq_files.

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	qqsearchv1 "packages/gen/qqsearch/v1"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// fileRow is the applied state of one raw QQ file.
type fileRow struct {
	botID            string
	conversationID   string
	conversationKind string
	externalUserID   string
	externalGroupID  string
	revision         int64
	status           RecordStatus
	uploaderUserID   string
	fileName         string
	mimeType         string
	sizeBytes        int64
	contentSHA256    string
	storageRef       string
	uploadedAt       pgtype.Timestamptz
}

// fileRowStatement reads one file row for update.
const fileRowStatement = `
SELECT bot_id, conversation_id, conversation_kind, external_user_id, external_group_id,
       record_revision, status, uploader_external_user_id, file_name, mime_type,
       size_bytes, content_sha256, storage_ref, uploaded_at
  FROM qq_search.qq_files
 WHERE record_id = $1
   FOR UPDATE`

// fileStateStatement is the reported state of one file. Its trailing
// "WHERE record_id = $1" is also the head of the scoped lookup below, so the two
// can never diverge in projection.
const fileStateStatement = `
SELECT record_id, bot_id, conversation_id, record_revision, status, indexed_at
  FROM qq_search.qq_files
 WHERE record_id = $1`

// fileStateInScopeQuery composes the file state lookup with the effective channel
// scope, using the file table's own columns. It is the mirror image of
// messageStateInScopeQuery and shares no clause with it: a message can never be
// matched by a file lookup, and vice versa.
func fileStateInScopeQuery(recordID string, scope channelScope) (string, []any) {
	args := []any{recordID}
	clauses := make([]string, 0, 3)
	if len(scope.botIDs) > 0 {
		args = append(args, scope.botIDs)
		clauses = append(clauses, "bot_id = ANY($"+strconv.Itoa(len(args))+"::text[])")
	}
	if len(scope.conversationIDs) > 0 {
		args = append(args, scope.conversationIDs)
		clauses = append(clauses, "conversation_id = ANY($"+strconv.Itoa(len(args))+"::text[])")
	}
	if len(scope.externalGroupIDs) > 0 {
		args = append(args, scope.externalGroupIDs)
		clauses = append(clauses, "external_group_id = ANY($"+strconv.Itoa(len(args))+"::text[])")
	}
	if len(clauses) == 0 {
		// Same fail-closed rule as the message lookup: a scope that constrains
		// nothing must match nothing, never every conversation.
		return fileStateStatement + " AND FALSE", args
	}
	return fileStateStatement + " AND " + strings.Join(clauses, " AND "), args
}

// loadFileRow reads one file row for update.
func loadFileRow(ctx context.Context, tx pgx.Tx, recordID string) (*fileRow, bool, error) {
	row := &fileRow{}
	var state string
	err := tx.QueryRow(ctx, fileRowStatement, recordID).Scan(
		&row.botID, &row.conversationID, &row.conversationKind, &row.externalUserID, &row.externalGroupID,
		&row.revision, &state, &row.uploaderUserID, &row.fileName, &row.mimeType,
		&row.sizeBytes, &row.contentSHA256, &row.storageRef, &row.uploadedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	row.status = RecordStatus(state)
	return row, true, nil
}

// loadFileState reads the reported state of one file without locking it.
func loadFileState(ctx context.Context, q querier, recordID string) (*qqsearchv1.QQRecordState, bool, error) {
	return scanFileState(ctx, q, fileStateStatement, recordID)
}

// loadFileStateInScope reads the reported state of one file, but only when the
// row lies inside the effective channel scope. An out-of-scope row is reported
// exactly like a missing one: (nil, false, nil).
func loadFileStateInScope(ctx context.Context, q querier, recordID string, scope channelScope) (*qqsearchv1.QQRecordState, bool, error) {
	statement, args := fileStateInScopeQuery(recordID, scope)
	return scanFileState(ctx, q, statement, args...)
}

// scanFileState reads one reported file state through whatever statement the
// caller composed.
func scanFileState(ctx context.Context, q querier, statement string, args ...any) (*qqsearchv1.QQRecordState, bool, error) {
	var (
		scannedID string
		botID     string
		convID    string
		revision  int64
		state     string
		indexedAt time.Time
	)
	err := q.QueryRow(ctx, statement, args...).Scan(&scannedID, &botID, &convID, &revision, &state, &indexedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &qqsearchv1.QQRecordState{
		RecordId:       scannedID,
		Kind:           qqsearchv1.QQRecordKind_QQ_RECORD_KIND_FILE,
		BotId:          botID,
		ConversationId: convID,
		RecordRevision: uint64(revision),
		Status:         protoStatus(RecordStatus(state)),
		IndexedAt:      indexedAt.UTC().Format(time.RFC3339Nano),
	}, true, nil
}

// fileUpsertStatement writes a file_upsert. uploaded_at is coalesced for the same
// reason sent_at is on the message side: an event that omits the instant must not
// erase one the source already supplied. The statement names qq_search.qq_files
// and no other table, and it touches file columns only. Inside ON CONFLICT DO
// UPDATE the existing row is referenced by the table's own name, the form
// PostgreSQL documents for that clause.
const fileUpsertStatement = `
INSERT INTO qq_search.qq_files (
    record_id, channel, bot_id, conversation_id, conversation_kind,
    external_user_id, external_group_id, uploader_external_user_id,
    file_name, mime_type, size_bytes, content_sha256, storage_ref,
    uploaded_at, record_revision, status, recalled_at, last_event_id,
    indexed_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15,
        'indexed', NULL, $16::uuid, clock_timestamp(), clock_timestamp())
ON CONFLICT (record_id) DO UPDATE SET
    channel = EXCLUDED.channel,
    bot_id = EXCLUDED.bot_id,
    conversation_id = EXCLUDED.conversation_id,
    conversation_kind = EXCLUDED.conversation_kind,
    external_user_id = EXCLUDED.external_user_id,
    external_group_id = EXCLUDED.external_group_id,
    uploader_external_user_id = EXCLUDED.uploader_external_user_id,
    file_name = EXCLUDED.file_name,
    mime_type = EXCLUDED.mime_type,
    size_bytes = EXCLUDED.size_bytes,
    content_sha256 = EXCLUDED.content_sha256,
    storage_ref = EXCLUDED.storage_ref,
    uploaded_at = COALESCE(EXCLUDED.uploaded_at, qq_files.uploaded_at),
    record_revision = EXCLUDED.record_revision,
    status = 'indexed',
    recalled_at = NULL,
    last_event_id = EXCLUDED.last_event_id,
    indexed_at = clock_timestamp(),
    updated_at = clock_timestamp()`

// fileRecallStatement records a file_recalled fact. The row stays, marked
// recalled, even when the recall arrives before the upload it refers to.
const fileRecallStatement = `
INSERT INTO qq_search.qq_files (
    record_id, channel, bot_id, conversation_id, conversation_kind,
    external_user_id, external_group_id, uploader_external_user_id,
    file_name, mime_type, size_bytes, content_sha256, storage_ref,
    uploaded_at, record_revision, status, recalled_at, last_event_id,
    indexed_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, '', '', '', 0, '', '', NULL, $8,
        'recalled', COALESCE($9::timestamptz, clock_timestamp()), $10::uuid, clock_timestamp(), clock_timestamp())
ON CONFLICT (record_id) DO UPDATE SET
    channel = EXCLUDED.channel,
    bot_id = EXCLUDED.bot_id,
    conversation_id = EXCLUDED.conversation_id,
    conversation_kind = EXCLUDED.conversation_kind,
    external_user_id = EXCLUDED.external_user_id,
    external_group_id = EXCLUDED.external_group_id,
    record_revision = EXCLUDED.record_revision,
    status = 'recalled',
    recalled_at = COALESCE($9::timestamptz, clock_timestamp()),
    last_event_id = EXCLUDED.last_event_id,
    updated_at = clock_timestamp()`

// upsertFile applies a file_upsert through the file model's statement.
func (service *Service) upsertFile(ctx context.Context, tx pgx.Tx, event *sourceEvent) error {
	_, err := tx.Exec(ctx, fileUpsertStatement,
		event.recordID, event.channel, event.botID, event.conversationID, event.conversationKind,
		event.externalUserID, event.externalGroupID, event.uploaderUserID,
		event.fileName, event.mimeType, event.sizeBytes, event.contentSHA256, event.storageRef,
		event.uploadedAt, event.revision, event.ledgerEventID,
	)
	return err
}

// recallFile applies a file_recalled through the file model's statement.
func (service *Service) recallFile(ctx context.Context, tx pgx.Tx, event *sourceEvent) error {
	_, err := tx.Exec(ctx, fileRecallStatement,
		event.recordID, event.channel, event.botID, event.conversationID, event.conversationKind,
		event.externalUserID, event.externalGroupID, event.revision,
		event.recallTimestamp(), event.ledgerEventID,
	)
	return err
}

// fileSearchHead is the projection half of the file query. The fallback arm of
// the WHERE clause below is a substring match accelerated by
// idx_qq_files_name_trgm (gin_trgm_ops), because a file name is usually a
// hyphenated blob in which a lexeme match is too strict.
const fileSearchHead = `
SELECT record_id, bot_id, conversation_id, uploader_external_user_id,
       file_name, mime_type, size_bytes, uploaded_at,
       GREATEST(
           ts_rank_cd(to_tsvector('simple', coalesce(file_name, '') || ' ' || coalesce(mime_type, '')),
                      websearch_to_tsquery('simple', $1)),
           similarity(coalesce(file_name, ''), $1)
       )::float8 AS score
  FROM qq_search.qq_files
 WHERE `

// fileSearchTail is the deterministic ordering for files.
const fileSearchTail = `
 ORDER BY score DESC, uploaded_at DESC NULLS LAST, record_id ASC
 LIMIT $`

// searchFileHits runs a file-only query. It reads qq_search.qq_files and nothing
// else, so a message can never appear in its result set.
func (service *Service) searchFileHits(ctx context.Context, query string, scope channelScope, limit int) ([]*qqsearchv1.QQFileHit, bool, error) {
	sql, args := fileSearchQuery(query, scope, limit)
	rows, err := service.pool.Pgx().Query(ctx, sql, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	hits := make([]*qqsearchv1.QQFileHit, 0, limit)
	for rows.Next() {
		var (
			recordID       string
			botID          string
			conversationID string
			uploader       string
			fileName       string
			mimeType       string
			sizeBytes      int64
			uploadedAt     pgtype.Timestamptz
			score          float64
		)
		if err := rows.Scan(&recordID, &botID, &conversationID, &uploader, &fileName, &mimeType,
			&sizeBytes, &uploadedAt, &score); err != nil {
			return nil, false, err
		}
		hits = append(hits, buildFileHit(recordID, botID, conversationID, uploader, fileName, mimeType, sizeBytes, uploadedAt, score))
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	truncated := len(hits) > limit
	if truncated {
		hits = hits[:limit]
	}
	return hits, truncated, nil
}

// buildFileHit assembles one hit from the file model's own columns.
func buildFileHit(recordID, botID, conversationID, uploader, fileName, mimeType string, sizeBytes int64, uploadedAt pgtype.Timestamptz, score float64) *qqsearchv1.QQFileHit {
	hit := &qqsearchv1.QQFileHit{
		RecordId:               recordID,
		BotId:                  botID,
		ConversationId:         conversationID,
		UploaderExternalUserId: uploader,
		FileName:               fileName,
		MimeType:               mimeType,
		SizeBytes:              sizeBytes,
		Score:                  score,
		Recalled:               false,
	}
	if uploadedAt.Valid {
		hit.UploadedAt = uploadedAt.Time.UTC().Format(time.RFC3339Nano)
	}
	return hit
}

// fileSearchQuery builds the file statement. The user's query reaches it as a
// parameter (and, for the substring arm, as an escaped LIKE pattern), never as
// SQL.
func fileSearchQuery(query string, scope channelScope, limit int) (string, []any) {
	args := []any{query, "%" + escapeLikePattern(query) + "%"}
	clauses := []string{
		"status = 'indexed'",
		"(to_tsvector('simple', coalesce(file_name, '') || ' ' || coalesce(mime_type, '')) @@ websearch_to_tsquery('simple', $1)" +
			" OR file_name ILIKE $2)",
	}
	if len(scope.botIDs) > 0 {
		args = append(args, scope.botIDs)
		clauses = append(clauses, "bot_id = ANY($"+strconv.Itoa(len(args))+"::text[])")
	}
	if len(scope.conversationIDs) > 0 {
		args = append(args, scope.conversationIDs)
		clauses = append(clauses, "conversation_id = ANY($"+strconv.Itoa(len(args))+"::text[])")
	}
	if len(scope.externalGroupIDs) > 0 {
		args = append(args, scope.externalGroupIDs)
		clauses = append(clauses, "external_group_id = ANY($"+strconv.Itoa(len(args))+"::text[])")
	}
	args = append(args, limit+1)
	return fileSearchHead + strings.Join(clauses, " AND ") + fileSearchTail + strconv.Itoa(len(args)), args
}

// escapeLikePattern neutralizes the LIKE wildcards in a user query, so searching
// for "100%" looks for that literal text instead of matching every file.
func escapeLikePattern(value string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value)
}
