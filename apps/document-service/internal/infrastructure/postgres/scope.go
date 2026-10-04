package postgres

import (
	"context"
	"database/sql"
	"encoding/json"

	"document-service/internal/domain"

	"github.com/jackc/pgx/v5"
)

// resolveScopeSQL is one statement on purpose: a single statement sees one
// snapshot, so a concurrent rebind can never produce "the old binding with the new
// membership". The whole read additionally runs at REPEATABLE READ for the same
// reason.
//
// A nil bot id or group id (or a private conversation, which passes an empty group
// id) makes every group subquery return nothing, which is exactly "no session
// group here".
const resolveScopeSQL = `
SELECT
    EXISTS (
        SELECT 1 FROM document_service.access_subjects s WHERE s.subject_key = $1
    ) AS subject_seen,
    COALESCE((
        SELECT s.active FROM document_service.access_subjects s WHERE s.subject_key = $1
    ), false) AS subject_active,
    (
        SELECT g.space_id::text
        FROM document_service.group_space_bindings g
        WHERE g.channel = $4 AND g.bot_id = $2 AND g.external_group_id = $3 AND g.revoked_at IS NULL
        LIMIT 1
    ) AS active_group_space_id,
    EXISTS (
        SELECT 1 FROM document_service.group_space_bindings g
        WHERE g.channel = $4 AND g.bot_id = $2 AND g.external_group_id = $3
    ) AS group_seen,
    EXISTS (
        SELECT 1 FROM document_service.group_space_bindings g
        WHERE g.channel = $4 AND g.bot_id = $2 AND g.external_group_id = $3 AND g.revoked_at IS NOT NULL
    ) AS group_revoked,
    EXISTS (
        SELECT 1
        FROM document_service.group_space_bindings g
        JOIN document_service.space_members sm ON sm.space_id = g.space_id
        WHERE g.channel = $4 AND g.bot_id = $2 AND g.external_group_id = $3 AND g.revoked_at IS NULL
          AND sm.subject_key = $1 AND sm.revoked_at IS NULL
    ) AS is_active_member,
    COALESCE((
        SELECT json_agg(ks.space_id::text ORDER BY ks.space_id::text)
        FROM document_service.knowledge_spaces ks
        WHERE ks.space_type = 'private' AND ks.owner_subject_key = $1
    ), '[]'::json)::text AS private_space_ids,
    COALESCE((
        SELECT json_agg(ks.space_id::text ORDER BY ks.space_id::text)
        FROM document_service.knowledge_spaces ks
        JOIN document_service.space_members sm ON sm.space_id = ks.space_id
        WHERE ks.space_type = 'team' AND sm.subject_key = $1 AND sm.revoked_at IS NULL
    ), '[]'::json)::text AS team_space_ids,
    COALESCE((
        SELECT json_agg(DISTINCT g.document_id::text)
        FROM document_service.document_grants g
        WHERE g.subject_type = 'subject' AND g.grantee_subject_key = $1 AND g.revoked_at IS NULL
    ), '[]'::json)::text AS document_ids`

// ResolveScopeFacts reads the resource facts for one subject in one session
// context. The Bot namespace is supplied by the caller: a QQ group identity only
// exists inside the Bot that saw it.
func (store *Store) ResolveScopeFacts(ctx context.Context, subjectKey, botID, channel, externalGroupID string) (domain.ScopeFacts, error) {
	var facts domain.ScopeFacts
	var activeGroupSpaceID sql.NullString
	var privateIDs string
	var teamIDs string
	var documentIDs string

	err := store.InConsistentRead(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return mapError(tx.QueryRow(ctx, resolveScopeSQL,
			subjectKey, nullableText(botID), nullableText(externalGroupID), channel,
		).Scan(
			&facts.SubjectSeen,
			&facts.SubjectActive,
			&activeGroupSpaceID,
			&facts.GroupSeen,
			&facts.GroupRevoked,
			&facts.IsActiveMember,
			&privateIDs,
			&teamIDs,
			&documentIDs,
		))
	})
	if err != nil {
		return domain.ScopeFacts{}, err
	}
	facts.ActiveGroupSpaceID = activeGroupSpaceID.String
	if facts.PrivateSpaceIDs, err = decodeIDList(privateIDs); err != nil {
		return domain.ScopeFacts{}, err
	}
	if facts.TeamSpaceIDs, err = decodeIDList(teamIDs); err != nil {
		return domain.ScopeFacts{}, err
	}
	if facts.DocumentIDs, err = decodeIDList(documentIDs); err != nil {
		return domain.ScopeFacts{}, err
	}
	return facts, nil
}

func decodeIDList(raw string) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	var ids []string
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return nil, mapError(err)
	}
	return ids, nil
}
