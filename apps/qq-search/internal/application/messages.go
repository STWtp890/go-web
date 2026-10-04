package application

// This file owns the raw QQ *message* model: its row shape, its two lifecycle
// transitions (upsert, recall) and its retrieval statement. There is no file
// column, no file status and no file collection anywhere in it, and files.go is
// the mirror image. Sharing either file would make a recall reachable from the
// wrong model.

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

// messageRow is the applied state of one raw QQ message. It carries exactly the
// columns the message model needs to decide "same revision, same payload?".
type messageRow struct {
	botID            string
	conversationID   string
	conversationKind string
	externalUserID   string
	externalGroupID  string
	revision         int64
	status           RecordStatus
	senderExternalID string
	text             string
	sentAt           pgtype.Timestamptz
	platformSequence int64
}

// loadMessageRow reads one message row for update. The row lock is what makes
// the revision fence a decision instead of a race.
func loadMessageRow(ctx context.Context, tx pgx.Tx, recordID string) (*messageRow, bool, error) {
	row := &messageRow{}
	var state string
	err := tx.QueryRow(ctx, messageRowStatement, recordID).Scan(
		&row.botID, &row.conversationID, &row.conversationKind, &row.externalUserID, &row.externalGroupID,
		&row.revision, &state, &row.senderExternalID, &row.text, &row.sentAt, &row.platformSequence,
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

// loadMessageState reads the reported state of one message without locking it.
func loadMessageState(ctx context.Context, q querier, recordID string) (*qqsearchv1.QQRecordState, bool, error) {
	return scanMessageState(ctx, q, messageStateStatement, recordID)
}

// loadMessageStateInScope reads the reported state of one message, but only when
// the row lies inside the effective channel scope. The scope is applied as
// predicates on this model's own columns, so an out-of-scope row is never read:
// it comes back as (nil, false, nil), identical to a row that does not exist.
func loadMessageStateInScope(ctx context.Context, q querier, recordID string, scope channelScope) (*qqsearchv1.QQRecordState, bool, error) {
	statement, args := messageStateInScopeQuery(recordID, scope)
	return scanMessageState(ctx, q, statement, args...)
}

// scanMessageState reads one reported message state through whatever statement
// the caller composed. It is the single place the message projection is scanned,
// so the unscoped and scoped lookups cannot drift apart in shape.
func scanMessageState(ctx context.Context, q querier, statement string, args ...any) (*qqsearchv1.QQRecordState, bool, error) {
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
		Kind:           qqsearchv1.QQRecordKind_QQ_RECORD_KIND_MESSAGE,
		BotId:          botID,
		ConversationId: convID,
		RecordRevision: uint64(revision),
		Status:         protoStatus(RecordStatus(state)),
		IndexedAt:      indexedAt.UTC().Format(time.RFC3339Nano),
	}, true, nil
}

// messageUpsertStatement writes a message_upsert. A newer revision replaces the
// stored content and clears a previous recall: a higher revision is a newer source
// fact. sent_at is coalesced because an event that omits the instant must not
// erase one the source already supplied.
//
// The statement names qq_search.qq_messages and no other table, and it touches
// message columns only. It is a package constant so the model-separation test can
// assert exactly that. Inside ON CONFLICT DO UPDATE the existing row is referenced
// by the table's own name (the form PostgreSQL documents for that clause); every
// object reference outside it stays schema-qualified.
const messageUpsertStatement = `
INSERT INTO qq_search.qq_messages (
    record_id, channel, bot_id, conversation_id, conversation_kind,
    external_user_id, external_group_id, sender_external_user_id,
    text_content, sent_at, platform_sequence, record_revision,
    status, recalled_at, last_event_id, indexed_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12,
        'indexed', NULL, $13::uuid, clock_timestamp(), clock_timestamp())
ON CONFLICT (record_id) DO UPDATE SET
    channel = EXCLUDED.channel,
    bot_id = EXCLUDED.bot_id,
    conversation_id = EXCLUDED.conversation_id,
    conversation_kind = EXCLUDED.conversation_kind,
    external_user_id = EXCLUDED.external_user_id,
    external_group_id = EXCLUDED.external_group_id,
    sender_external_user_id = EXCLUDED.sender_external_user_id,
    text_content = EXCLUDED.text_content,
    sent_at = COALESCE(EXCLUDED.sent_at, qq_messages.sent_at),
    platform_sequence = EXCLUDED.platform_sequence,
    record_revision = EXCLUDED.record_revision,
    status = 'indexed',
    recalled_at = NULL,
    last_event_id = EXCLUDED.last_event_id,
    indexed_at = clock_timestamp(),
    updated_at = clock_timestamp()`

// messageRecallStatement records a message_recalled fact. The row is never
// deleted: a recall that arrives before its own upsert still creates the row
// (marked recalled, with no content), so a later out-of-order upsert at a lower
// revision is fenced by the ledger instead of resurrecting the message.
const messageRecallStatement = `
INSERT INTO qq_search.qq_messages (
    record_id, channel, bot_id, conversation_id, conversation_kind,
    external_user_id, external_group_id, sender_external_user_id,
    text_content, sent_at, platform_sequence, record_revision,
    status, recalled_at, last_event_id, indexed_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, '', '', NULL, 0, $8,
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

// messageStateStatement is the reported state of one message. Its trailing
// "WHERE record_id = $1" is also the head of the scoped lookup below, so the two
// can never diverge in projection.
const messageStateStatement = `
SELECT record_id, bot_id, conversation_id, record_revision, status, indexed_at
  FROM qq_search.qq_messages
 WHERE record_id = $1`

// messageStateInScopeQuery composes the message state lookup with the effective
// channel scope. Every identifier family the scope constrains becomes a
// predicate on this model's own column, exactly as in messageSearchQuery: the
// filter is part of the statement, so a row outside the scope is never read and
// cannot be reported. No family is ever widened: an open family adds no
// predicate, and a scope that constrains nothing matches nothing.
func messageStateInScopeQuery(recordID string, scope channelScope) (string, []any) {
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
		// The caller refuses an empty scope before reaching the database. Should
		// that invariant ever break, the lookup must match nothing rather than
		// degrade into "every conversation".
		return messageStateStatement + " AND FALSE", args
	}
	return messageStateStatement + " AND " + strings.Join(clauses, " AND "), args
}

// messageRowStatement reads one message row for update.
const messageRowStatement = `
SELECT bot_id, conversation_id, conversation_kind, external_user_id, external_group_id,
       record_revision, status, sender_external_user_id, text_content, sent_at, platform_sequence
  FROM qq_search.qq_messages
 WHERE record_id = $1
   FOR UPDATE`

// upsertMessage applies a message_upsert through the message model's statement.
func (service *Service) upsertMessage(ctx context.Context, tx pgx.Tx, event *sourceEvent) error {
	_, err := tx.Exec(ctx, messageUpsertStatement,
		event.recordID, event.channel, event.botID, event.conversationID, event.conversationKind,
		event.externalUserID, event.externalGroupID, event.senderExternalID,
		event.messageText, event.sentAt, event.platformSequence, event.revision,
		event.ledgerEventID,
	)
	return err
}

// recallMessage applies a message_recalled through the message model's statement.
func (service *Service) recallMessage(ctx context.Context, tx pgx.Tx, event *sourceEvent) error {
	_, err := tx.Exec(ctx, messageRecallStatement,
		event.recordID, event.channel, event.botID, event.conversationID, event.conversationKind,
		event.externalUserID, event.externalGroupID, event.revision,
		event.recallTimestamp(), event.ledgerEventID,
	)
	return err
}

// messageSearchHead is the projection half of the message query. The tsvector
// expression matches idx_qq_messages_search exactly, so the GIN index is usable.
const messageSearchHead = `
SELECT record_id, bot_id, conversation_id, sender_external_user_id, text_content, sent_at,
       ts_rank_cd(to_tsvector('simple', coalesce(text_content, '')), websearch_to_tsquery('simple', $1))::float8 AS score
  FROM qq_search.qq_messages
 WHERE `

// messageSearchTail is the deterministic ordering: score, then the source
// instant, then the record id. No two runs can order two equal-scoring hits
// differently.
const messageSearchTail = `
 ORDER BY score DESC, sent_at DESC NULLS LAST, record_id ASC
 LIMIT $`

// searchMessageHits runs a message-only query. It reads qq_search.qq_messages
// and nothing else: a file can never appear in its result set, because the file
// table is not part of the statement.
func (service *Service) searchMessageHits(ctx context.Context, query string, scope channelScope, limit int) ([]*qqsearchv1.QQMessageHit, bool, error) {
	sql, args := messageSearchQuery(query, scope, limit)
	rows, err := service.pool.Pgx().Query(ctx, sql, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	hits := make([]*qqsearchv1.QQMessageHit, 0, limit)
	for rows.Next() {
		var (
			recordID       string
			botID          string
			conversationID string
			sender         string
			text           string
			sentAt         pgtype.Timestamptz
			score          float64
		)
		if err := rows.Scan(&recordID, &botID, &conversationID, &sender, &text, &sentAt, &score); err != nil {
			return nil, false, err
		}
		hits = append(hits, buildMessageHit(recordID, botID, conversationID, sender, text, sentAt, score))
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

// buildMessageHit assembles one hit. Only indexed rows are ever selected, so
// recalled is structurally false here; it is filled in explicitly so the field
// can never be read as "true means hidden".
func buildMessageHit(recordID, botID, conversationID, sender, text string, sentAt pgtype.Timestamptz, score float64) *qqsearchv1.QQMessageHit {
	hit := &qqsearchv1.QQMessageHit{
		RecordId:             recordID,
		BotId:                botID,
		ConversationId:       conversationID,
		SenderExternalUserId: sender,
		Text:                 text,
		Score:                score,
		Recalled:             false,
	}
	if sentAt.Valid {
		hit.SentAt = sentAt.Time.UTC().Format(time.RFC3339Nano)
	}
	return hit
}

// messageSearchQuery builds the message statement. Only literal SQL fragments
// are concatenated; every identifier comes from a parameter, so the channel
// scope can never become SQL.
func messageSearchQuery(query string, scope channelScope, limit int) (string, []any) {
	args := []any{query}
	clauses := []string{
		"status = 'indexed'",
		"to_tsvector('simple', coalesce(text_content, '')) @@ websearch_to_tsquery('simple', $1)",
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
	return messageSearchHead + strings.Join(clauses, " AND ") + messageSearchTail + strconv.Itoa(len(args)), args
}
