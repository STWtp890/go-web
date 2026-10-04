package postgres

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"document-service/internal/domain"

	"github.com/jackc/pgx/v5"
)

const insertSpaceSQL = `
INSERT INTO document_service.knowledge_spaces (space_id, owner_subject_key, space_type, name, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6)`

// InsertSpace creates one knowledge space. The owner membership is inserted by
// the caller in the same transaction: the deferred constraint trigger refuses a
// space without an active owner.
func (store *Store) InsertSpace(ctx context.Context, tx pgx.Tx, space domain.Space) error {
	_, err := tx.Exec(ctx, insertSpaceSQL,
		space.SpaceID, space.OwnerSubjectKey, space.SpaceType, space.Name, space.CreatedAt, space.UpdatedAt,
	)
	return mapError(err)
}

// Columns are cast to text so a uuid is always read as a canonical string.
const spaceColumns = `space_id::text, owner_subject_key, space_type, name, created_at, updated_at`

const selectSpaceSQL = `
SELECT ` + spaceColumns + `
FROM document_service.knowledge_spaces
WHERE space_id = $1::text::uuid`

// GetSpace reads one space. A missing space is ErrNotFound.
func (store *Store) GetSpace(ctx context.Context, tx pgx.Tx, spaceID string) (domain.Space, error) {
	return scanSpace(tx.QueryRow(ctx, selectSpaceSQL, spaceID))
}

const selectSpaceForUpdateSQL = selectSpaceSQL + ` FOR UPDATE`

// LockSpace reads one space FOR UPDATE, so "is this a team space" and "who owns
// it" cannot change while a membership or binding decision is made.
func (store *Store) LockSpace(ctx context.Context, tx pgx.Tx, spaceID string) (domain.Space, error) {
	return scanSpace(tx.QueryRow(ctx, selectSpaceForUpdateSQL, spaceID))
}

const selectPrivateSpaceSQL = `
SELECT ` + spaceColumns + `
FROM document_service.knowledge_spaces
WHERE owner_subject_key = $1 AND space_type = 'private'
LIMIT 1`

// FindPrivateSpace returns the subject's private space. The second result is
// false when the subject has none yet; a private space is created lazily with the
// first document, so "absent" is a normal state and not an error.
func (store *Store) FindPrivateSpace(ctx context.Context, tx pgx.Tx, ownerSubjectKey string) (domain.Space, bool, error) {
	space, err := scanSpace(tx.QueryRow(ctx, selectPrivateSpaceSQL, ownerSubjectKey))
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Space{}, false, nil
	}
	if err != nil {
		return domain.Space{}, false, err
	}
	return space, true, nil
}

const lockPrivateSpaceSQL = `
SELECT pg_advisory_xact_lock(hashtext('document_service:private_space'), hashtext($1::text))`

// LockPrivateSpaceOwner serializes lazy private-space creation for one subject.
// The partial unique index is the last line of defence; this lock keeps two
// concurrent creates from turning into a retry loop.
func (store *Store) LockPrivateSpaceOwner(ctx context.Context, tx pgx.Tx, ownerSubjectKey string) error {
	_, err := tx.Exec(ctx, lockPrivateSpaceSQL, ownerSubjectKey)
	return mapError(err)
}

func scanSpace(row subjectScanner) (domain.Space, error) {
	var space domain.Space
	if err := row.Scan(
		&space.SpaceID, &space.OwnerSubjectKey, &space.SpaceType, &space.Name,
		&space.CreatedAt, &space.UpdatedAt,
	); err != nil {
		return domain.Space{}, mapError(err)
	}
	return space, nil
}

const insertMemberSQL = `
INSERT INTO document_service.space_members (space_id, subject_key, member_role, created_at, updated_at)
VALUES ($1::text::uuid, $2, $3, $4, $5)`

// InsertMember adds one active membership row.
func (store *Store) InsertMember(ctx context.Context, tx pgx.Tx, member domain.Member) error {
	_, err := tx.Exec(ctx, insertMemberSQL,
		member.SpaceID, member.SubjectKey, member.MemberRole, member.CreatedAt, member.UpdatedAt,
	)
	return mapError(err)
}

const memberColumns = `space_id::text, subject_key, member_role, created_at, updated_at, revoked_at`

const selectMemberSQL = `
SELECT ` + memberColumns + `
FROM document_service.space_members
WHERE space_id = $1::text::uuid AND subject_key = $2`

// GetMember reads the membership row for a subject, active or revoked. The second
// result is false when the subject never had a row, which is what separates
// "restore" from "create".
func (store *Store) GetMember(ctx context.Context, tx pgx.Tx, spaceID, subjectKey string) (domain.Member, bool, error) {
	member, err := scanMember(tx.QueryRow(ctx, selectMemberSQL, spaceID, subjectKey))
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Member{}, false, nil
	}
	if err != nil {
		return domain.Member{}, false, err
	}
	return member, true, nil
}

const restoreMemberSQL = `
UPDATE document_service.space_members
SET member_role = $3, revoked_at = NULL, updated_at = $4
WHERE space_id = $1::text::uuid AND subject_key = $2 AND revoked_at IS NOT NULL`

// RestoreMember re-activates a revoked membership row. It reports whether a
// revoked row was actually restored.
func (store *Store) RestoreMember(ctx context.Context, tx pgx.Tx, spaceID, subjectKey, role string, now time.Time) (bool, error) {
	tag, err := tx.Exec(ctx, restoreMemberSQL, spaceID, subjectKey, role, now)
	if err != nil {
		return false, mapError(err)
	}
	return tag.RowsAffected() == 1, nil
}

const revokeMemberSQL = `
UPDATE document_service.space_members
SET revoked_at = $3, updated_at = $3
WHERE space_id = $1::text::uuid AND subject_key = $2 AND revoked_at IS NULL`

// RevokeMember closes an active membership. It reports whether an active row was
// actually closed, so the caller can answer NOT_FOUND instead of pretending.
func (store *Store) RevokeMember(ctx context.Context, tx pgx.Tx, spaceID, subjectKey string, now time.Time) (bool, error) {
	tag, err := tx.Exec(ctx, revokeMemberSQL, spaceID, subjectKey, now)
	if err != nil {
		return false, mapError(err)
	}
	return tag.RowsAffected() == 1, nil
}

const listMembersSQL = `
SELECT ` + memberColumns + `
FROM document_service.space_members
WHERE space_id = $1::text::uuid
ORDER BY revoked_at NULLS FIRST, subject_key`

// ListMembers reads every membership row of a space, active rows first.
func (store *Store) ListMembers(ctx context.Context, tx pgx.Tx, spaceID string) ([]domain.Member, error) {
	rows, err := tx.Query(ctx, listMembersSQL, spaceID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	members := make([]domain.Member, 0, 8)
	for rows.Next() {
		member, err := scanMember(rows)
		if err != nil {
			return nil, err
		}
		members = append(members, member)
	}
	if err := rows.Err(); err != nil {
		return nil, mapError(err)
	}
	return members, nil
}

func scanMember(row subjectScanner) (domain.Member, error) {
	var member domain.Member
	var revokedAt sql.NullTime
	if err := row.Scan(
		&member.SpaceID, &member.SubjectKey, &member.MemberRole,
		&member.CreatedAt, &member.UpdatedAt, &revokedAt,
	); err != nil {
		return domain.Member{}, mapError(err)
	}
	if revokedAt.Valid {
		value := revokedAt.Time
		member.RevokedAt = &value
	}
	return member, nil
}
