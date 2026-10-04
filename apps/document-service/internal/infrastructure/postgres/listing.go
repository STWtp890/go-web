package postgres

import (
	"context"
	"database/sql"
	"time"

	"document-service/internal/domain"

	"github.com/jackc/pgx/v5"
)

// readableDocumentPredicate is the SQL half of "may this subject read this
// document". The decision itself is made in the application layer from the facts
// this predicate exposes (see LoadAccessFacts); the listing uses the predicate
// directly so a page never contains a document the caller may not see.
//
// Four independent ways in, exactly as the contract states: ownership, a subject
// level grant, active membership of the owner space or of a granted space, and
// authenticated_public for a registered, active subject.
const readableDocumentPredicate = `
    d.owner_subject_key = $1
    OR EXISTS (
        SELECT 1 FROM document_service.document_grants g
        WHERE g.document_id = d.document_id AND g.revoked_at IS NULL
          AND g.subject_type = 'subject' AND g.grantee_subject_key = $1
    )
    OR EXISTS (
        SELECT 1 FROM document_service.document_grants g
        JOIN document_service.space_members sm ON sm.space_id = g.grantee_space_id
        WHERE g.document_id = d.document_id AND g.revoked_at IS NULL
          AND g.subject_type = 'space' AND sm.subject_key = $1 AND sm.revoked_at IS NULL
    )
    OR EXISTS (
        SELECT 1 FROM document_service.space_members sm
        WHERE sm.space_id = d.owner_space_id AND sm.subject_key = $1 AND sm.revoked_at IS NULL
    )
    OR (
        EXISTS (
            SELECT 1 FROM document_service.document_access_policies p
            WHERE p.document_id = d.document_id AND p.authenticated_public
        )
        AND EXISTS (
            SELECT 1 FROM document_service.access_subjects s
            WHERE s.subject_key = $1 AND s.active
        )
    )`

// DocumentFilter is the bounded, permission-filtered listing query.
type DocumentFilter struct {
	// SubjectKey is the authenticated caller subject; the result never contains a
	// document this subject may not read.
	SubjectKey string
	// OwnerSubjectKey, SpaceID and LifecycleStatus are optional narrowing filters.
	OwnerSubjectKey string
	SpaceID         string
	LifecycleStatus string
	// AuthenticatedPublicOnly restricts the page to documents whose access policy
	// is authenticated public. It is a filter on top of the permission predicate,
	// never a grant: the caller still has to be allowed to read what it matches.
	AuthenticatedPublicOnly bool
	// CursorTime and CursorDocumentID are the keyset position of the previous
	// page. They are both set or both zero.
	CursorTime       time.Time
	CursorDocumentID string
	Limit            int
}

// documentFilterSQL is the shared WHERE body of the listing page and its total
// count. Keeping it in one constant is what guarantees the page and the reported
// total can never disagree about which documents match: the count is the same
// predicate without the keyset cursor and the page limit.
//
// Parameters: $1 subject, $2 owner subject, $3 space, $4 lifecycle,
// $5 authenticated_public_only.
const documentFilterSQL = `(` + readableDocumentPredicate + `
)
  AND ($2::text IS NULL OR d.owner_subject_key = $2)
  AND (
      $3::text IS NULL
      OR d.owner_space_id = $3::text::uuid
      OR EXISTS (
          SELECT 1 FROM document_service.document_grants g2
          WHERE g2.document_id = d.document_id AND g2.revoked_at IS NULL
            AND g2.subject_type = 'space' AND g2.grantee_space_id = $3::text::uuid
      )
  )
  AND ($4::text IS NULL OR d.lifecycle_status = $4)
  AND (
      NOT $5::boolean
      OR EXISTS (
          SELECT 1 FROM document_service.document_access_policies p3
          WHERE p3.document_id = d.document_id AND p3.authenticated_public
      )
  )`

const listDocumentsSQL = `
SELECT
    d.document_id::text,
    d.owner_subject_key,
    d.owner_space_id::text,
    d.lifecycle_status,
    COALESCE(d.active_version_id::text, ''),
    COALESCE(v.title, ''),
    COALESCE(v.publication_status, ''),
    d.aggregate_revision,
    d.created_at,
    d.updated_at,
    COALESCE(p.authenticated_public, false)
FROM document_service.documents d
LEFT JOIN LATERAL (
    SELECT dv.version_id, dv.title, dv.publication_status
    FROM document_service.document_versions dv
    WHERE dv.document_id = d.document_id
    ORDER BY (dv.version_id = d.active_version_id) DESC NULLS LAST, dv.revision DESC
    LIMIT 1
) v ON TRUE
LEFT JOIN document_service.document_access_policies p ON p.document_id = d.document_id
WHERE ` + documentFilterSQL + `
  AND (
      $6::timestamptz IS NULL
      OR (d.created_at, d.document_id) < ($6::timestamptz, $7::text::uuid)
  )
ORDER BY d.created_at DESC, d.document_id DESC
LIMIT $8`

// countReadableDocumentsSQL counts every document matching the same filter as the
// page, independent of the page returned.
const countReadableDocumentsSQL = `
SELECT count(*)
FROM document_service.documents d
WHERE ` + documentFilterSQL

// ListReadableDocuments returns one page of document summaries the subject may
// read, newest first.
func (store *Store) ListReadableDocuments(ctx context.Context, tx pgx.Tx, filter DocumentFilter) ([]domain.Summary, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 20
	}
	var cursorTime any
	var cursorDocumentID any
	if !filter.CursorTime.IsZero() && filter.CursorDocumentID != "" {
		cursorTime = filter.CursorTime
		cursorDocumentID = filter.CursorDocumentID
	}

	rows, err := tx.Query(ctx, listDocumentsSQL,
		filter.SubjectKey,
		nullableText(filter.OwnerSubjectKey),
		nullableText(filter.SpaceID),
		nullableText(filter.LifecycleStatus),
		filter.AuthenticatedPublicOnly,
		cursorTime,
		cursorDocumentID,
		limit,
	)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	summaries := make([]domain.Summary, 0, limit)
	for rows.Next() {
		var summary domain.Summary
		if err := rows.Scan(
			&summary.DocumentID, &summary.OwnerSubjectKey, &summary.OwnerSpaceID,
			&summary.LifecycleStatus, &summary.ActiveVersionID, &summary.Title,
			&summary.PublicationStatus, &summary.AggregateRevision,
			&summary.CreatedAt, &summary.UpdatedAt, &summary.AuthenticatedPublic,
		); err != nil {
			return nil, mapError(err)
		}
		summaries = append(summaries, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, mapError(err)
	}
	return summaries, nil
}

// CountReadableDocuments returns how many documents match the listing filter,
// independent of the page that was returned. It runs the same predicate as the
// page query, so an honest total cannot drift from the rows.
func (store *Store) CountReadableDocuments(ctx context.Context, tx pgx.Tx, filter DocumentFilter) (int64, error) {
	var total int64
	if err := tx.QueryRow(ctx, countReadableDocumentsSQL,
		filter.SubjectKey,
		nullableText(filter.OwnerSubjectKey),
		nullableText(filter.SpaceID),
		nullableText(filter.LifecycleStatus),
		filter.AuthenticatedPublicOnly,
	).Scan(&total); err != nil {
		return 0, mapError(err)
	}
	return total, nil
}

// AccessFacts is the raw material of the document read decision. It contains
// facts only: the rule that turns them into allow or deny lives in the domain
// layer (domain.MayReadDocument), so it is testable without a database.
const loadAccessFactsSQL = `
SELECT
    d.owner_subject_key = $2 AS is_owner,
    EXISTS (
        SELECT 1 FROM document_service.document_grants g
        WHERE g.document_id = d.document_id AND g.revoked_at IS NULL
          AND g.subject_type = 'subject' AND g.grantee_subject_key = $2
    ) AS has_subject_grant,
    EXISTS (
        SELECT 1 FROM document_service.document_grants g
        JOIN document_service.space_members sm ON sm.space_id = g.grantee_space_id
        WHERE g.document_id = d.document_id AND g.revoked_at IS NULL
          AND g.subject_type = 'space' AND sm.subject_key = $2 AND sm.revoked_at IS NULL
    ) AS is_granted_space_member,
    EXISTS (
        SELECT 1 FROM document_service.space_members sm
        WHERE sm.space_id = d.owner_space_id AND sm.subject_key = $2 AND sm.revoked_at IS NULL
    ) AS is_owner_space_member,
    COALESCE(p.authenticated_public, false) AS authenticated_public,
    s.subject_key IS NOT NULL AS subject_registered,
    COALESCE(s.active, false) AS subject_active,
    d.document_id::text,
    d.owner_subject_key,
    d.owner_space_id::text,
    d.lifecycle_status,
    COALESCE(d.active_version_id::text, ''),
    d.activation_revision,
    d.access_revision,
    d.lifecycle_revision,
    d.aggregate_revision,
    d.created_at,
    d.updated_at,
    d.trashed_at
FROM document_service.documents d
LEFT JOIN document_service.document_access_policies p ON p.document_id = d.document_id
LEFT JOIN document_service.access_subjects s ON s.subject_key = $2
WHERE d.document_id = $1::text::uuid`

// LoadAccessFacts reads every fact the read decision depends on in one
// statement, so the decision is made from a single consistent snapshot.
func (store *Store) LoadAccessFacts(ctx context.Context, tx pgx.Tx, documentID, subjectKey string) (domain.AccessFacts, error) {
	var facts domain.AccessFacts
	var trashedAt sql.NullTime
	err := tx.QueryRow(ctx, loadAccessFactsSQL, documentID, subjectKey).Scan(
		&facts.IsOwner,
		&facts.HasSubjectGrant,
		&facts.IsGrantedSpaceMember,
		&facts.IsOwnerSpaceMember,
		&facts.AuthenticatedPublic,
		&facts.SubjectRegistered,
		&facts.SubjectActive,
		&facts.DocumentID,
		&facts.OwnerSubjectKey,
		&facts.OwnerSpaceID,
		&facts.LifecycleStatus,
		&facts.ActiveVersionID,
		&facts.ActivationRevision,
		&facts.AccessRevision,
		&facts.LifecycleRevision,
		&facts.AggregateRevision,
		&facts.CreatedAt,
		&facts.UpdatedAt,
		&trashedAt,
	)
	if err != nil {
		return domain.AccessFacts{}, mapError(err)
	}
	if trashedAt.Valid {
		value := trashedAt.Time
		facts.TrashedAt = &value
	}
	return facts, nil
}
