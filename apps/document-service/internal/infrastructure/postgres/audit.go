package postgres

import (
	"context"
	"errors"
	"strings"

	"document-service/internal/domain"

	"github.com/jackc/pgx/v5"
)

// AuditSink appends one audit row inside the caller's transaction. It is an
// interface rather than a plain method so a test can substitute a failing sink
// and prove that a failed audit insert rolls the business mutation back instead
// of leaving an unaudited change behind.
type AuditSink interface {
	Append(ctx context.Context, tx pgx.Tx, event domain.AuditEvent) error
}

// DefaultAuditSink writes document_service.space_audit_events.
type DefaultAuditSink struct{}

const insertAuditSQL = `
INSERT INTO document_service.space_audit_events (
    event_id, subject_type, action, subject_key, channel, bot_id, external_id,
    space_id, previous_target, new_target, actor, source, reason, request_id, occurred_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`

// Append writes one audit row. The table is append-only; there is no update or
// delete path anywhere in this service.
func (DefaultAuditSink) Append(ctx context.Context, tx pgx.Tx, event domain.AuditEvent) error {
	if tx == nil {
		return errors.New("document-service postgres: audit requires a transaction")
	}
	_, err := tx.Exec(ctx, insertAuditSQL,
		event.EventID,
		event.SubjectType,
		event.Action,
		nullableText(event.SubjectKey),
		nullableText(event.Channel),
		nullableText(event.BotID),
		nullableText(event.ExternalID),
		nullableText(event.SpaceID),
		nullableText(event.PreviousTarget),
		nullableText(event.NewTarget),
		event.Actor,
		event.Source,
		event.Reason,
		event.RequestID,
		event.OccurredAt,
	)
	if err != nil {
		return mapError(err)
	}
	return nil
}

// AppendAudit routes one audit row through the configured sink.
func (store *Store) AppendAudit(ctx context.Context, tx pgx.Tx, event domain.AuditEvent) error {
	if store == nil || store.audit == nil {
		return errors.New("document-service postgres: audit sink is not configured")
	}
	return store.audit.Append(ctx, tx, event)
}

// nullableText maps an empty string onto SQL NULL for the optional audit columns.
func nullableText(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}
