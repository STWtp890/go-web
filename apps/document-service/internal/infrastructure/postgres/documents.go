package postgres

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"document-service/internal/domain"

	"github.com/jackc/pgx/v5"
)

const documentColumns = `document_id::text, owner_subject_key, owner_space_id::text, lifecycle_status,
	COALESCE(active_version_id::text, ''), activation_revision, access_revision, lifecycle_revision,
	aggregate_revision, COALESCE(create_request_id, ''), created_at, updated_at, trashed_at`

const insertDocumentSQL = `
INSERT INTO document_service.documents (
    document_id, owner_subject_key, owner_space_id, lifecycle_status, active_version_id,
    activation_revision, access_revision, lifecycle_revision, aggregate_revision,
    create_request_id, created_at, updated_at, trashed_at
) VALUES ($1::text::uuid, $2, $3::text::uuid, $4, NULL, $5, $6, $7, $8, $9, $10, $11, $12)`

// InsertDocument writes the aggregate head of a new document. The active version
// is attached afterwards, inside the same transaction, because the version row
// must exist before the deferred foreign key is validated.
func (store *Store) InsertDocument(ctx context.Context, tx pgx.Tx, document domain.Document) error {
	var trashedAt any
	if document.TrashedAt != nil {
		trashedAt = *document.TrashedAt
	}
	_, err := tx.Exec(ctx, insertDocumentSQL,
		document.DocumentID, document.OwnerSubjectKey, document.OwnerSpaceID, document.LifecycleStatus,
		document.ActivationRevision, document.AccessRevision, document.LifecycleRevision,
		document.AggregateRevision, nullableText(document.CreateRequestID),
		document.CreatedAt, document.UpdatedAt, trashedAt,
	)
	return mapError(err)
}

const selectDocumentSQL = `
SELECT ` + documentColumns + `
FROM document_service.documents
WHERE document_id = $1::text::uuid`

// GetDocument reads one document head.
func (store *Store) GetDocument(ctx context.Context, tx pgx.Tx, documentID string) (domain.Document, error) {
	return scanDocument(tx.QueryRow(ctx, selectDocumentSQL, documentID))
}

// LockDocument reads one document head FOR UPDATE. Every mutating command starts
// here, so two concurrent commands serialize on the aggregate instead of both
// deciding from the same stale revision.
func (store *Store) LockDocument(ctx context.Context, tx pgx.Tx, documentID string) (domain.Document, error) {
	return scanDocument(tx.QueryRow(ctx, selectDocumentSQL+` FOR UPDATE`, documentID))
}

const selectDocumentByCreateRequestSQL = `
SELECT ` + documentColumns + `
FROM document_service.documents
WHERE owner_subject_key = $1 AND create_request_id = $2
LIMIT 1`

// FindDocumentByCreateRequest implements create idempotency: the same subject
// reusing the same request id gets the document it already created instead of a
// second one.
func (store *Store) FindDocumentByCreateRequest(ctx context.Context, tx pgx.Tx, ownerSubjectKey, requestID string) (domain.Document, bool, error) {
	document, err := scanDocument(tx.QueryRow(ctx, selectDocumentByCreateRequestSQL, ownerSubjectKey, requestID))
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Document{}, false, nil
	}
	if err != nil {
		return domain.Document{}, false, err
	}
	return document, true, nil
}

const updateDocumentStateSQL = `
UPDATE document_service.documents
SET lifecycle_status = $2,
    active_version_id = $3::text::uuid,
    activation_revision = $4,
    access_revision = $5,
    lifecycle_revision = $6,
    aggregate_revision = $7,
    trashed_at = $8,
    updated_at = $9
WHERE document_id = $1::text::uuid`

// UpdateDocumentState persists the new aggregate head produced by one of the
// domain transitions. All four revisions are written together so a caller can
// never observe a half-applied transition. The idempotency ledger of the save
// command lives in its own table and is written by InsertSaveRequest, in the same
// transaction as the version it names.
func (store *Store) UpdateDocumentState(ctx context.Context, tx pgx.Tx, document domain.Document) error {
	var activeVersion any
	if document.ActiveVersionID != "" {
		activeVersion = document.ActiveVersionID
	}
	var trashedAt any
	if document.TrashedAt != nil {
		trashedAt = *document.TrashedAt
	}
	_, err := tx.Exec(ctx, updateDocumentStateSQL,
		document.DocumentID, document.LifecycleStatus, activeVersion,
		document.ActivationRevision, document.AccessRevision, document.LifecycleRevision,
		document.AggregateRevision, trashedAt, document.UpdatedAt,
	)
	return mapError(err)
}

func scanDocument(row subjectScanner) (domain.Document, error) {
	var document domain.Document
	var trashedAt sql.NullTime
	if err := row.Scan(
		&document.DocumentID, &document.OwnerSubjectKey, &document.OwnerSpaceID,
		&document.LifecycleStatus, &document.ActiveVersionID, &document.ActivationRevision,
		&document.AccessRevision, &document.LifecycleRevision, &document.AggregateRevision,
		&document.CreateRequestID, &document.CreatedAt, &document.UpdatedAt, &trashedAt,
	); err != nil {
		return domain.Document{}, mapError(err)
	}
	if trashedAt.Valid {
		value := trashedAt.Time
		document.TrashedAt = &value
	}
	return document, nil
}

const versionColumns = `version_id::text, document_id::text, revision, publication_status, title, summary,
	content, content_format, content_sha256, created_by_subject_key, created_at`

const insertVersionSQL = `
INSERT INTO document_service.document_versions (
    version_id, document_id, revision, publication_status, title, summary,
    content, content_format, content_sha256, created_by_subject_key, created_at
) VALUES ($1::text::uuid, $2::text::uuid, $3, $4, $5, $6, $7, $8, $9, $10, $11)`

// InsertVersion appends one immutable content revision.
func (store *Store) InsertVersion(ctx context.Context, tx pgx.Tx, version domain.Version) error {
	_, err := tx.Exec(ctx, insertVersionSQL,
		version.VersionID, version.DocumentID, version.Revision, version.PublicationStatus,
		version.Title, version.Summary, version.Content, version.ContentFormat,
		version.ContentSHA256, version.CreatedBySubjectKey, version.CreatedAt,
	)
	return mapError(err)
}

const selectVersionSQL = `
SELECT ` + versionColumns + `
FROM document_service.document_versions
WHERE version_id = $1::text::uuid AND document_id = $2::text::uuid`

// GetVersion reads one version of one document. Addressing a version through its
// document is what keeps a version id from another document from being accepted.
func (store *Store) GetVersion(ctx context.Context, tx pgx.Tx, documentID, versionID string) (domain.Version, error) {
	return scanVersion(tx.QueryRow(ctx, selectVersionSQL, versionID, documentID))
}

const selectLatestVersionSQL = `
SELECT ` + versionColumns + `
FROM document_service.document_versions
WHERE document_id = $1::text::uuid
ORDER BY revision DESC
LIMIT 1`

// GetLatestVersion reads the highest numbered version. The second result is false
// for a document that has no version yet.
func (store *Store) GetLatestVersion(ctx context.Context, tx pgx.Tx, documentID string) (domain.Version, bool, error) {
	version, err := scanVersion(tx.QueryRow(ctx, selectLatestVersionSQL, documentID))
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Version{}, false, nil
	}
	if err != nil {
		return domain.Version{}, false, err
	}
	return version, true, nil
}

const maxVersionRevisionSQL = `
SELECT COALESCE(MAX(revision), 0)
FROM document_service.document_versions
WHERE document_id = $1::text::uuid`

// MaxVersionRevision returns the highest revision number of a document, or 0.
func (store *Store) MaxVersionRevision(ctx context.Context, tx pgx.Tx, documentID string) (int64, error) {
	var revision int64
	if err := tx.QueryRow(ctx, maxVersionRevisionSQL, documentID).Scan(&revision); err != nil {
		return 0, mapError(err)
	}
	return revision, nil
}

const updateVersionStatusSQL = `
UPDATE document_service.document_versions
SET publication_status = $2
WHERE version_id = $1::text::uuid`

// SetVersionPublicationStatus moves one version between draft, published,
// superseded and withdrawn. The content columns are immutable and the database
// trigger enforces that.
func (store *Store) SetVersionPublicationStatus(ctx context.Context, tx pgx.Tx, versionID, status string) error {
	tag, err := tx.Exec(ctx, updateVersionStatusSQL, versionID, status)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrNotFound
	}
	return nil
}

func scanVersion(row subjectScanner) (domain.Version, error) {
	var version domain.Version
	if err := row.Scan(
		&version.VersionID, &version.DocumentID, &version.Revision, &version.PublicationStatus,
		&version.Title, &version.Summary, &version.Content, &version.ContentFormat,
		&version.ContentSHA256, &version.CreatedBySubjectKey, &version.CreatedAt,
	); err != nil {
		return domain.Version{}, mapError(err)
	}
	return version, nil
}

const putAccessPolicySQL = `
INSERT INTO document_service.document_access_policies (
    document_id, authenticated_public, access_revision, updated_at
) VALUES ($1::text::uuid, $2, $3, $4)
ON CONFLICT (document_id) DO UPDATE
SET authenticated_public = EXCLUDED.authenticated_public,
    access_revision = EXCLUDED.access_revision,
    updated_at = EXCLUDED.updated_at`

// PutAccessPolicy writes the document level access flag together with its
// revision.
func (store *Store) PutAccessPolicy(ctx context.Context, tx pgx.Tx, policy domain.AccessPolicy) error {
	_, err := tx.Exec(ctx, putAccessPolicySQL,
		policy.DocumentID, policy.AuthenticatedPublic, policy.AccessRevision, policy.UpdatedAt,
	)
	return mapError(err)
}

const selectAccessPolicySQL = `
SELECT document_id::text, authenticated_public, access_revision, updated_at
FROM document_service.document_access_policies
WHERE document_id = $1::text::uuid`

// GetAccessPolicy reads the access flag. The second result is false when no
// policy row exists, which is different from "exists and is false".
func (store *Store) GetAccessPolicy(ctx context.Context, tx pgx.Tx, documentID string) (domain.AccessPolicy, bool, error) {
	var policy domain.AccessPolicy
	err := tx.QueryRow(ctx, selectAccessPolicySQL, documentID).Scan(
		&policy.DocumentID, &policy.AuthenticatedPublic, &policy.AccessRevision, &policy.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.AccessPolicy{}, false, nil
	}
	if err != nil {
		return domain.AccessPolicy{}, false, mapError(err)
	}
	return policy, true, nil
}

const upsertDocumentSourceSQL = `
INSERT INTO document_service.document_sources (
    document_id, origin, bot_id, conversation_id, source_record_id, created_at
) VALUES ($1::text::uuid, $2, $3, $4, $5, $6)
ON CONFLICT (document_id) DO UPDATE
SET origin = EXCLUDED.origin,
    bot_id = EXCLUDED.bot_id,
    conversation_id = EXCLUDED.conversation_id,
    source_record_id = EXCLUDED.source_record_id`

// PutDocumentSource records where promoted content came from. A duplicate source
// record id is refused by the partial unique index and surfaces as
// ErrAlreadyExists, which the caller reports as ALREADY_EXISTS.
func (store *Store) PutDocumentSource(ctx context.Context, tx pgx.Tx, documentID string, source domain.Source, now time.Time) error {
	_, err := tx.Exec(ctx, upsertDocumentSourceSQL,
		documentID, source.Origin, source.BotID, source.ConversationID, source.SourceRecordID, now,
	)
	return mapError(err)
}

const selectDocumentSourceSQL = `
SELECT origin, bot_id, conversation_id, source_record_id
FROM document_service.document_sources
WHERE document_id = $1::text::uuid`

// GetDocumentSource reads the origin trace. The second result is false when the
// document was not promoted from a raw source.
func (store *Store) GetDocumentSource(ctx context.Context, tx pgx.Tx, documentID string) (domain.Source, bool, error) {
	var source domain.Source
	err := tx.QueryRow(ctx, selectDocumentSourceSQL, documentID).Scan(
		&source.Origin, &source.BotID, &source.ConversationID, &source.SourceRecordID,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Source{}, false, nil
	}
	if err != nil {
		return domain.Source{}, false, mapError(err)
	}
	return source, true, nil
}

const listGrantedSpaceIDsSQL = `
SELECT grantee_space_id::text
FROM document_service.document_grants
WHERE document_id = $1::text::uuid AND subject_type = 'space' AND revoked_at IS NULL
ORDER BY 1`

// ListGrantedSpaceIDs reads the space level grants of a document. They are part
// of the access snapshot every Outbox event carries.
func (store *Store) ListGrantedSpaceIDs(ctx context.Context, tx pgx.Tx, documentID string) ([]string, error) {
	rows, err := tx.Query(ctx, listGrantedSpaceIDsSQL, documentID)
	if err != nil {
		return nil, mapError(err)
	}
	defer rows.Close()

	spaceIDs := make([]string, 0, 4)
	for rows.Next() {
		var spaceID string
		if err := rows.Scan(&spaceID); err != nil {
			return nil, mapError(err)
		}
		spaceIDs = append(spaceIDs, spaceID)
	}
	if err := rows.Err(); err != nil {
		return nil, mapError(err)
	}
	return spaceIDs, nil
}
