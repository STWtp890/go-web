package postgresql

import (
	"context"
	"time"

	"gin-backend/internal/modules/document/domain"

	"gorm.io/gorm"
)

var _ domain.QueryRepository = (*Repository)(nil)

type documentHeadRow struct {
	DocumentID          string
	OwnerID             int64
	ActiveVersionID     string
	AuthenticatedPublic bool
	ActivationRevision  int64
	AccessRevision      int64
	LifecycleRevision   int64
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type documentViewRow struct {
	DocumentID          string
	OwnerID             int64
	ActiveVersionID     string
	AuthenticatedPublic bool
	ActivationRevision  int64
	AccessRevision      int64
	LifecycleRevision   int64
	CreatedAt           time.Time
	UpdatedAt           time.Time
	Title               string
	Summary             string
	Content             string
}

type documentSummaryRow struct {
	DocumentID          string
	OwnerID             int64
	AuthenticatedPublic bool
	Title               string
	Summary             string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

func (repository *Repository) GetActiveDocumentHead(ctx context.Context, documentID string) (*domain.DocumentHead, error) {
	var row documentHeadRow
	err := repository.db.WithContext(ctx).
		Table("documents AS document").
		Select(`document.document_id, document.owner_id, document.active_version_id,
document.activation_revision, document.access_revision, document.lifecycle_revision,
document.created_at, document.updated_at, policy.authenticated_public`).
		Joins("JOIN document_access_policies AS policy ON policy.document_id = document.document_id").
		Where("document.document_id = ?", documentID).
		Where("document.lifecycle_status = ?", string(domain.LifecycleActive)).
		Where("document.active_version_id IS NOT NULL").
		Take(&row).Error
	if err != nil {
		return nil, wrapRepositoryError("get active document head", err)
	}
	return documentHeadFromRow(row), nil
}

func (repository *Repository) GetActiveDocumentView(ctx context.Context, head domain.DocumentHead) (*domain.DocumentView, error) {
	var row documentViewRow
	err := repository.db.WithContext(ctx).
		Table("documents AS document").
		Select(`document.document_id, document.owner_id, document.active_version_id,
document.activation_revision, document.access_revision, document.lifecycle_revision,
document.created_at, document.updated_at, policy.authenticated_public,
version.title, version.summary, version.content`).
		Joins("JOIN document_access_policies AS policy ON policy.document_id = document.document_id").
		Joins("JOIN document_versions AS version ON version.document_id = document.document_id AND version.version_id = document.active_version_id").
		Where("document.document_id = ?", head.DocumentID).
		Where("document.active_version_id = ?", head.ActiveVersionID).
		Where("document.activation_revision = ? AND document.access_revision = ? AND document.lifecycle_revision = ?", head.ActivationRevision, head.AccessRevision, head.LifecycleRevision).
		Where("document.lifecycle_status = ?", string(domain.LifecycleActive)).
		Where("version.publication_status = ?", string(domain.PublicationPublished)).
		Take(&row).Error
	if err != nil {
		return nil, wrapRepositoryError("get active document view", err)
	}
	return &domain.DocumentView{
		DocumentHead: domain.DocumentHead{
			DocumentID: row.DocumentID, OwnerID: row.OwnerID, ActiveVersionID: row.ActiveVersionID,
			AuthenticatedPublic: row.AuthenticatedPublic,
			ActivationRevision:  row.ActivationRevision, AccessRevision: row.AccessRevision,
			LifecycleRevision: row.LifecycleRevision, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		},
		Title: row.Title, Summary: row.Summary, Content: row.Content,
	}, nil
}

func (repository *Repository) ListOwnedDocuments(ctx context.Context, ownerID int64, offset, limit int) ([]domain.DocumentSummary, int64, error) {
	query := repository.activeProjectionQuery(ctx).Where("projection.owner_id = ?", ownerID)
	return repository.listDocumentSummaries(query, offset, limit, "projection.updated_at DESC, projection.document_id ASC")
}

func (repository *Repository) ListPublicDocuments(ctx context.Context, offset, limit int) ([]domain.DocumentSummary, int64, error) {
	query := repository.activeProjectionQuery(ctx).Where("projection.authenticated_public = ?", true)
	return repository.listDocumentSummaries(query, offset, limit, "projection.updated_at DESC, projection.document_id ASC")
}

func (repository *Repository) SearchOwnedDocuments(ctx context.Context, ownerID int64, keyword string, offset, limit int) ([]domain.DocumentSummary, int64, error) {
	query := repository.activeProjectionQuery(ctx).
		Where("projection.owner_id = ?", ownerID).
		Where("projection.search_text ||| ?", keyword)
	return repository.listDocumentSummaries(query, offset, limit, "pdb.score(projection.document_id) DESC, projection.document_id ASC")
}

func (repository *Repository) activeProjectionQuery(ctx context.Context) *gorm.DB {
	return repository.db.WithContext(ctx).
		Table("document_search_projection AS projection").
		Joins(`JOIN documents AS document
ON document.document_id = projection.document_id
AND document.active_version_id = projection.version_id
AND document.activation_revision = projection.activation_revision
AND document.access_revision = projection.access_revision
AND document.lifecycle_revision = projection.lifecycle_revision`).
		Where("document.lifecycle_status = ?", string(domain.LifecycleActive))
}

func (repository *Repository) listDocumentSummaries(query *gorm.DB, offset, limit int, order string) ([]domain.DocumentSummary, int64, error) {
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var rows []documentSummaryRow
	err := query.
		Select(`projection.document_id, projection.owner_id, projection.authenticated_public,
projection.title, projection.summary, document.created_at, projection.updated_at`).
		Order(order).
		Offset(offset).
		Limit(limit).
		Scan(&rows).Error
	if err != nil {
		return nil, 0, err
	}

	items := make([]domain.DocumentSummary, 0, len(rows))
	for _, row := range rows {
		items = append(items, domain.DocumentSummary{
			DocumentID: row.DocumentID, OwnerID: row.OwnerID,
			AuthenticatedPublic: row.AuthenticatedPublic,
			Title:               row.Title, Summary: row.Summary, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		})
	}
	return items, total, nil
}

func documentHeadFromRow(row documentHeadRow) *domain.DocumentHead {
	return &domain.DocumentHead{
		DocumentID: row.DocumentID, OwnerID: row.OwnerID, ActiveVersionID: row.ActiveVersionID,
		AuthenticatedPublic: row.AuthenticatedPublic,
		ActivationRevision:  row.ActivationRevision, AccessRevision: row.AccessRevision,
		LifecycleRevision: row.LifecycleRevision, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}
