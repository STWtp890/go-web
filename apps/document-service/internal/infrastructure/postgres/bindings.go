package postgres

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"document-service/internal/domain"

	"github.com/jackc/pgx/v5"
)

const bindingColumns = `binding_id::text, channel, bot_id, external_group_id, space_id::text, actor, source, reason, created_at, revoked_at`

const insertBindingSQL = `
INSERT INTO document_service.group_space_bindings (
    binding_id, channel, bot_id, external_group_id, space_id, actor, source, reason, created_at
) VALUES ($1::text::uuid, $2, $3, $4, $5::text::uuid, $6, $7, $8, $9)`

// InsertBinding appends one active group to space binding. Rebinding closes the
// previous row and inserts here, so history is never overwritten.
func (store *Store) InsertBinding(ctx context.Context, tx pgx.Tx, binding domain.Binding) error {
	_, err := tx.Exec(ctx, insertBindingSQL,
		binding.BindingID, binding.Channel, binding.BotID, binding.ExternalGroupID,
		binding.SpaceID, binding.Actor, binding.Source, binding.Reason, binding.CreatedAt,
	)
	return mapError(err)
}

const selectActiveGroupBindingSQL = `
SELECT ` + bindingColumns + `
FROM document_service.group_space_bindings
WHERE channel = $1 AND bot_id = $2 AND external_group_id = $3 AND revoked_at IS NULL
LIMIT 1`

// GetActiveGroupBinding resolves the space bound to a group. The second result is
// false when the group has no active binding.
func (store *Store) GetActiveGroupBinding(ctx context.Context, tx pgx.Tx, channel, botID, externalGroupID string) (domain.Binding, bool, error) {
	binding, err := scanBinding(tx.QueryRow(ctx, selectActiveGroupBindingSQL, channel, botID, externalGroupID))
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Binding{}, false, nil
	}
	if err != nil {
		return domain.Binding{}, false, err
	}
	return binding, true, nil
}

const selectActiveSpaceBindingSQL = `
SELECT ` + bindingColumns + `
FROM document_service.group_space_bindings
WHERE channel = $1 AND bot_id = $2 AND space_id = $3::text::uuid AND revoked_at IS NULL
LIMIT 1`

// GetActiveSpaceBinding resolves the group currently bound to a space under one
// Bot. One team space may be bound to at most one group per Bot.
func (store *Store) GetActiveSpaceBinding(ctx context.Context, tx pgx.Tx, channel, botID, spaceID string) (domain.Binding, bool, error) {
	binding, err := scanBinding(tx.QueryRow(ctx, selectActiveSpaceBindingSQL, channel, botID, spaceID))
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Binding{}, false, nil
	}
	if err != nil {
		return domain.Binding{}, false, err
	}
	return binding, true, nil
}

const selectLatestGroupBindingSQL = `
SELECT ` + bindingColumns + `
FROM document_service.group_space_bindings
WHERE channel = $1 AND bot_id = $2 AND external_group_id = $3
ORDER BY created_at DESC, binding_id DESC
LIMIT 1`

// GetLatestGroupBinding reads the newest binding row for a group, active or
// revoked. It is what lets the resolver say "revoked" instead of merely
// "unbound".
func (store *Store) GetLatestGroupBinding(ctx context.Context, tx pgx.Tx, channel, botID, externalGroupID string) (domain.Binding, bool, error) {
	binding, err := scanBinding(tx.QueryRow(ctx, selectLatestGroupBindingSQL, channel, botID, externalGroupID))
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Binding{}, false, nil
	}
	if err != nil {
		return domain.Binding{}, false, err
	}
	return binding, true, nil
}

const revokeBindingSQL = `
UPDATE document_service.group_space_bindings
SET revoked_at = $2
WHERE binding_id = $1::text::uuid AND revoked_at IS NULL`

// RevokeBinding closes one active binding and reports whether an active row was
// actually closed.
func (store *Store) RevokeBinding(ctx context.Context, tx pgx.Tx, bindingID string, now time.Time) (bool, error) {
	tag, err := tx.Exec(ctx, revokeBindingSQL, bindingID, now)
	if err != nil {
		return false, mapError(err)
	}
	return tag.RowsAffected() == 1, nil
}

const listActiveBindingsSQL = `
SELECT ` + bindingColumns + `
FROM document_service.group_space_bindings
WHERE space_id = $1::text::uuid AND revoked_at IS NULL
ORDER BY created_at, binding_id`

// ListActiveBindings reads the active group bindings of a space.
func (store *Store) ListActiveBindings(ctx context.Context, tx pgx.Tx, spaceID string) ([]domain.Binding, error) {
	rows, err := tx.Query(ctx, listActiveBindingsSQL, spaceID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	bindings := make([]domain.Binding, 0, 4)
	for rows.Next() {
		binding, err := scanBinding(rows)
		if err != nil {
			return nil, err
		}
		bindings = append(bindings, binding)
	}
	if err := rows.Err(); err != nil {
		return nil, mapError(err)
	}
	return bindings, nil
}

const lockGroupBindingSQL = `
SELECT pg_advisory_xact_lock(hashtext('document_service:group_binding'), hashtext($1::text))`

// LockGroupBinding serializes the read-decide-write sequence of a binding
// mutation for one group, so two concurrent binds produce one active row instead
// of a race decided by the unique index alone.
func (store *Store) LockGroupBinding(ctx context.Context, tx pgx.Tx, channel, botID, externalGroupID string) error {
	_, err := tx.Exec(ctx, lockGroupBindingSQL, channel+"|"+botID+"|"+externalGroupID)
	return mapError(err)
}

func scanBinding(row subjectScanner) (domain.Binding, error) {
	var binding domain.Binding
	var revokedAt sql.NullTime
	if err := row.Scan(
		&binding.BindingID, &binding.Channel, &binding.BotID, &binding.ExternalGroupID,
		&binding.SpaceID, &binding.Actor, &binding.Source, &binding.Reason,
		&binding.CreatedAt, &revokedAt,
	); err != nil {
		return domain.Binding{}, mapError(err)
	}
	if revokedAt.Valid {
		value := revokedAt.Time
		binding.RevokedAt = &value
	}
	return binding, nil
}
