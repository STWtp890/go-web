// Package postgresql 实现文档领域的 PostgreSQL/GORM 仓储。
// 数据库结构只由 PostgreSQL 空卷初始化脚本管理，本包不调用 AutoMigrate。
package postgresql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	domain "gin-backend/internal/modules/document/domain"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Repository struct {
	db *gorm.DB
}

var _ domain.Repository = (*Repository)(nil)

func New(db *gorm.DB) (*Repository, error) {
	if db == nil {
		return nil, errors.New("document repository: database is nil")
	}
	return &Repository{db: db}, nil
}

func (repository *Repository) InTransaction(ctx context.Context, fn func(domain.Repository) error) error {
	if fn == nil {
		return errors.New("document repository: transaction callback is nil")
	}
	return repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(&Repository{db: tx})
	})
}

func (repository *Repository) InRepeatableRead(ctx context.Context, fn func(domain.Repository) error) error {
	if fn == nil {
		return errors.New("document repository: repeatable-read callback is nil")
	}
	return repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(&Repository{db: tx})
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
}

func (repository *Repository) EnsurePrivateSpace(ctx context.Context, entity *domain.KnowledgeSpace) (bool, error) {
	if entity == nil {
		return false, errors.New("ensure private space: entity is nil")
	}
	entity.SpaceType = domain.SpaceTypePrivate
	record := knowledgeSpaceToRecord(entity)
	result := repository.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(record)
	if result.Error != nil {
		return false, fmt.Errorf("ensure private space: %w", result.Error)
	}
	if result.RowsAffected == 1 {
		entity.CreatedAt, entity.UpdatedAt = record.CreatedAt, record.UpdatedAt
		return true, nil
	}

	var existing knowledgeSpaceRecord
	if err := repository.db.WithContext(ctx).
		Where("owner_id = ? AND space_type = ?", entity.OwnerID, string(domain.SpaceTypePrivate)).
		First(&existing).Error; err != nil {
		return false, wrapRepositoryError("get existing private space", err)
	}
	*entity = *knowledgeSpaceFromRecord(&existing)
	return false, nil
}
func (repository *Repository) CreateKnowledgeSpace(ctx context.Context, entity *domain.KnowledgeSpace) error {
	record := knowledgeSpaceToRecord(entity)
	if err := repository.db.WithContext(ctx).Create(record).Error; err != nil {
		return fmt.Errorf("create knowledge space: %w", err)
	}
	entity.CreatedAt, entity.UpdatedAt = record.CreatedAt, record.UpdatedAt
	return nil
}

func (repository *Repository) CreateSpaceMember(ctx context.Context, entity *domain.SpaceMember) error {
	record := spaceMemberToRecord(entity)
	if err := repository.db.WithContext(ctx).Create(record).Error; err != nil {
		return fmt.Errorf("create space member: %w", err)
	}
	entity.CreatedAt, entity.UpdatedAt = record.CreatedAt, record.UpdatedAt
	return nil
}

func (repository *Repository) CreateDocument(ctx context.Context, entity *domain.Document) error {
	if entity.LifecycleStatus == "" {
		entity.LifecycleStatus = domain.LifecycleActive
	}
	record := documentToRecord(entity)
	if err := repository.db.WithContext(ctx).Create(record).Error; err != nil {
		return fmt.Errorf("create document: %w", err)
	}
	entity.CreatedAt, entity.UpdatedAt = record.CreatedAt, record.UpdatedAt
	return nil
}

func (repository *Repository) CreateDocumentVersion(ctx context.Context, entity *domain.DocumentVersion) error {
	if entity.ContentFormat == "" {
		entity.ContentFormat = "markdown"
	}
	record := documentVersionToRecord(entity)
	if err := repository.db.WithContext(ctx).Create(record).Error; err != nil {
		return fmt.Errorf("create document version: %w", err)
	}
	entity.CreatedAt = record.CreatedAt
	return nil
}

func (repository *Repository) UpdateVersionPublicationStatus(ctx context.Context, versionID string, status domain.PublicationStatus) error {
	result := repository.db.WithContext(ctx).Model(&documentVersionRecord{}).
		Where("version_id = ?", versionID).
		Update("publication_status", string(status))
	if result.Error != nil {
		return fmt.Errorf("update document version publication status: %w", result.Error)
	}
	return requireAffectedRow(result)
}

func (repository *Repository) SetActiveVersion(ctx context.Context, documentID, versionID string, activationRevision, aggregateRevision int64) error {
	result := repository.db.WithContext(ctx).Model(&documentRecord{}).
		Where("document_id = ?", documentID).
		Updates(map[string]any{
			"active_version_id":   versionID,
			"activation_revision": activationRevision,
			"aggregate_revision":  aggregateRevision,
			"updated_at":          time.Now(),
		})
	if result.Error != nil {
		return fmt.Errorf("set active document version: %w", result.Error)
	}
	return requireAffectedRow(result)
}

func (repository *Repository) SetAccessRevision(ctx context.Context, documentID string, accessRevision, aggregateRevision int64) error {
	result := repository.db.WithContext(ctx).Model(&documentRecord{}).
		Where("document_id = ?", documentID).
		Updates(map[string]any{
			"access_revision":    accessRevision,
			"aggregate_revision": aggregateRevision,
			"updated_at":         time.Now(),
		})
	if result.Error != nil {
		return fmt.Errorf("set document access revision: %w", result.Error)
	}
	return requireAffectedRow(result)
}

func (repository *Repository) SetLifecycleState(ctx context.Context, documentID string, status domain.LifecycleStatus, lifecycleRevision, aggregateRevision int64, trashedAt *time.Time) error {
	result := repository.db.WithContext(ctx).Model(&documentRecord{}).
		Where("document_id = ?", documentID).
		Updates(map[string]any{
			"lifecycle_status":   string(status),
			"lifecycle_revision": lifecycleRevision,
			"aggregate_revision": aggregateRevision,
			"trashed_at":         trashedAt,
			"updated_at":         time.Now(),
		})
	if result.Error != nil {
		return fmt.Errorf("set document lifecycle state: %w", result.Error)
	}
	return requireAffectedRow(result)
}
func (repository *Repository) PutAccessPolicy(ctx context.Context, entity *domain.AccessPolicy) error {
	record := accessPolicyToRecord(entity)
	err := repository.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "document_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"authenticated_public", "access_revision", "updated_at"}),
	}).Create(record).Error
	if err != nil {
		return fmt.Errorf("put document access policy: %w", err)
	}
	entity.UpdatedAt = record.UpdatedAt
	return nil
}

func (repository *Repository) CreateGrant(ctx context.Context, entity *domain.DocumentGrant) error {
	record := documentGrantToRecord(entity)
	if err := repository.db.WithContext(ctx).Create(record).Error; err != nil {
		return fmt.Errorf("create document grant: %w", err)
	}
	entity.CreatedAt = record.CreatedAt
	return nil
}

func (repository *Repository) PutSearchProjection(ctx context.Context, entity *domain.SearchProjection) error {
	record := searchProjectionToRecord(entity)
	err := repository.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "document_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"version_id", "owner_id", "owner_space_id", "authenticated_public",
			"title", "summary", "search_text", "activation_revision", "access_revision",
			"lifecycle_revision", "updated_at",
		}),
	}).Create(record).Error
	if err != nil {
		return fmt.Errorf("put document search projection: %w", err)
	}
	entity.UpdatedAt = record.UpdatedAt
	return nil
}

func (repository *Repository) DeleteSearchProjection(ctx context.Context, documentID string) error {
	result := repository.db.WithContext(ctx).Where("document_id = ?", documentID).Delete(&searchProjectionRecord{})
	if result.Error != nil {
		return fmt.Errorf("delete document search projection: %w", result.Error)
	}
	return requireAffectedRow(result)
}

func (repository *Repository) AppendIndexDeliveryEvent(ctx context.Context, entity *domain.IndexDeliveryEvent) error {
	if entity == nil {
		return errors.New("append document index delivery event: entity is nil")
	}
	record, err := indexDeliveryEventToRecord(entity)
	if err != nil {
		return fmt.Errorf("append document index delivery event: %w", err)
	}
	if err := repository.db.WithContext(ctx).Create(record).Error; err != nil {
		return fmt.Errorf("append document index delivery event: %w", err)
	}
	entity.CreatedAt = record.CreatedAt
	entity.UpdatedAt = record.UpdatedAt
	return nil
}

func (repository *Repository) AppendIndexDeliveryEventIfAbsent(ctx context.Context, entity *domain.IndexDeliveryEvent) (bool, error) {
	if entity == nil {
		return false, errors.New("append document index delivery event if absent: entity is nil")
	}
	record, err := indexDeliveryEventToRecord(entity)
	if err != nil {
		return false, fmt.Errorf("append document index delivery event if absent: %w", err)
	}
	result := repository.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "dedupe_key"}}, DoNothing: true,
	}).Create(record)
	if result.Error != nil {
		return false, fmt.Errorf("append document index delivery event if absent: %w", result.Error)
	}
	if result.RowsAffected == 1 {
		entity.CreatedAt, entity.UpdatedAt = record.CreatedAt, record.UpdatedAt
		return true, nil
	}
	return false, nil
}

func (repository *Repository) GetDocument(ctx context.Context, documentID string) (*domain.Document, error) {
	var record documentRecord
	if err := repository.db.WithContext(ctx).Where("document_id = ?", documentID).First(&record).Error; err != nil {
		return nil, wrapRepositoryError("get document", err)
	}
	return documentFromRecord(&record), nil
}

func (repository *Repository) GetAccessPolicy(ctx context.Context, documentID string) (*domain.AccessPolicy, error) {
	var record accessPolicyRecord
	if err := repository.db.WithContext(ctx).Where("document_id = ?", documentID).First(&record).Error; err != nil {
		return nil, wrapRepositoryError("get document access policy", err)
	}
	return accessPolicyFromRecord(&record), nil
}

func (repository *Repository) GetDocumentVersion(ctx context.Context, versionID string) (*domain.DocumentVersion, error) {
	var record documentVersionRecord
	if err := repository.db.WithContext(ctx).Where("version_id = ?", versionID).First(&record).Error; err != nil {
		return nil, wrapRepositoryError("get document version", err)
	}
	return documentVersionFromRecord(&record), nil
}

func (repository *Repository) GetLatestDocumentVersion(ctx context.Context, documentID string) (*domain.DocumentVersion, error) {
	var record documentVersionRecord
	if err := repository.db.WithContext(ctx).
		Where("document_id = ?", documentID).
		Order("revision DESC").
		First(&record).Error; err != nil {
		return nil, wrapRepositoryError("get latest document version", err)
	}
	return documentVersionFromRecord(&record), nil
}
func (repository *Repository) ListDocumentVersions(ctx context.Context, documentID string) ([]domain.DocumentVersion, error) {
	var records []documentVersionRecord
	if err := repository.db.WithContext(ctx).
		Where("document_id = ?", documentID).
		Order("revision ASC").
		Find(&records).Error; err != nil {
		return nil, fmt.Errorf("list document versions: %w", err)
	}
	entities := make([]domain.DocumentVersion, 0, len(records))
	for index := range records {
		entities = append(entities, *documentVersionFromRecord(&records[index]))
	}
	return entities, nil
}

func (repository *Repository) ListDocumentsForIndexing(ctx context.Context, afterDocumentID string, limit int) ([]domain.Document, error) {
	if limit <= 0 {
		limit = 100
	}
	query := repository.db.WithContext(ctx).Order("document_id ASC").Limit(limit)
	if afterDocumentID != "" {
		query = query.Where("document_id > ?", afterDocumentID)
	}
	var records []documentRecord
	if err := query.Find(&records).Error; err != nil {
		return nil, fmt.Errorf("list documents for indexing: %w", err)
	}
	documents := make([]domain.Document, 0, len(records))
	for index := range records {
		documents = append(documents, *documentFromRecord(&records[index]))
	}
	return documents, nil
}

func (repository *Repository) ListActiveGrantedSpaceIDs(ctx context.Context, documentID string) ([]string, error) {
	var records []documentGrantRecord
	if err := repository.db.WithContext(ctx).
		Where("document_id = ? AND subject_type = ? AND revoked_at IS NULL", documentID, string(domain.GrantSubjectSpace)).
		Order("grantee_space_id ASC").
		Find(&records).Error; err != nil {
		return nil, fmt.Errorf("list active document space grants: %w", err)
	}
	spaceIDs := make([]string, 0, len(records))
	for index := range records {
		if records[index].GranteeSpaceID != nil {
			spaceIDs = append(spaceIDs, *records[index].GranteeSpaceID)
		}
	}
	return spaceIDs, nil
}

func (repository *Repository) LockDocument(ctx context.Context, documentID string) (*domain.Document, error) {
	var record documentRecord
	err := repository.db.WithContext(ctx).
		Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("document_id = ?", documentID).
		First(&record).Error
	if err != nil {
		return nil, wrapRepositoryError("lock document", err)
	}
	return documentFromRecord(&record), nil
}

func wrapRepositoryError(operation string, err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("%s: %w", operation, domain.ErrNotFound)
	}
	return fmt.Errorf("%s: %w", operation, err)
}
func requireAffectedRow(result *gorm.DB) error {
	if result.RowsAffected == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func knowledgeSpaceToRecord(entity *domain.KnowledgeSpace) *knowledgeSpaceRecord {
	return &knowledgeSpaceRecord{SpaceID: entity.SpaceID, OwnerID: entity.OwnerID, SpaceType: string(entity.SpaceType), Name: entity.Name, CreatedAt: entity.CreatedAt, UpdatedAt: entity.UpdatedAt}
}

func knowledgeSpaceFromRecord(record *knowledgeSpaceRecord) *domain.KnowledgeSpace {
	return &domain.KnowledgeSpace{SpaceID: record.SpaceID, OwnerID: record.OwnerID, SpaceType: domain.SpaceType(record.SpaceType), Name: record.Name, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt}
}
func spaceMemberToRecord(entity *domain.SpaceMember) *spaceMemberRecord {
	return &spaceMemberRecord{SpaceID: entity.SpaceID, UserID: entity.UserID, MemberRole: string(entity.MemberRole), CreatedAt: entity.CreatedAt, UpdatedAt: entity.UpdatedAt, RevokedAt: entity.RevokedAt}
}

func documentToRecord(entity *domain.Document) *documentRecord {
	return &documentRecord{DocumentID: entity.DocumentID, OwnerID: entity.OwnerID, OwnerSpaceID: entity.OwnerSpaceID, LifecycleStatus: string(entity.LifecycleStatus), ActiveVersionID: entity.ActiveVersionID, ActivationRevision: entity.ActivationRevision, AccessRevision: entity.AccessRevision, LifecycleRevision: entity.LifecycleRevision, AggregateRevision: entity.AggregateRevision, CreatedAt: entity.CreatedAt, UpdatedAt: entity.UpdatedAt, TrashedAt: entity.TrashedAt}
}

func documentFromRecord(record *documentRecord) *domain.Document {
	return &domain.Document{DocumentID: record.DocumentID, OwnerID: record.OwnerID, OwnerSpaceID: record.OwnerSpaceID, LifecycleStatus: domain.LifecycleStatus(record.LifecycleStatus), ActiveVersionID: record.ActiveVersionID, ActivationRevision: record.ActivationRevision, AccessRevision: record.AccessRevision, LifecycleRevision: record.LifecycleRevision, AggregateRevision: record.AggregateRevision, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt, TrashedAt: record.TrashedAt}
}

func documentVersionToRecord(entity *domain.DocumentVersion) *documentVersionRecord {
	return &documentVersionRecord{VersionID: entity.VersionID, DocumentID: entity.DocumentID, Revision: entity.Revision, PublicationStatus: string(entity.PublicationStatus), Title: entity.Title, Summary: entity.Summary, Content: entity.Content, ContentFormat: entity.ContentFormat, ContentSHA256: entity.ContentSHA256, CreatedBy: entity.CreatedBy, CreatedAt: entity.CreatedAt}
}

func documentVersionFromRecord(record *documentVersionRecord) *domain.DocumentVersion {
	return &domain.DocumentVersion{VersionID: record.VersionID, DocumentID: record.DocumentID, Revision: record.Revision, PublicationStatus: domain.PublicationStatus(record.PublicationStatus), Title: record.Title, Summary: record.Summary, Content: record.Content, ContentFormat: record.ContentFormat, ContentSHA256: record.ContentSHA256, CreatedBy: record.CreatedBy, CreatedAt: record.CreatedAt}
}

func accessPolicyToRecord(entity *domain.AccessPolicy) *accessPolicyRecord {
	return &accessPolicyRecord{DocumentID: entity.DocumentID, AuthenticatedPublic: entity.AuthenticatedPublic, AccessRevision: entity.AccessRevision, UpdatedAt: entity.UpdatedAt}
}

func accessPolicyFromRecord(record *accessPolicyRecord) *domain.AccessPolicy {
	return &domain.AccessPolicy{DocumentID: record.DocumentID, AuthenticatedPublic: record.AuthenticatedPublic, AccessRevision: record.AccessRevision, UpdatedAt: record.UpdatedAt}
}
func documentGrantToRecord(entity *domain.DocumentGrant) *documentGrantRecord {
	return &documentGrantRecord{GrantID: entity.GrantID, DocumentID: entity.DocumentID, SubjectType: string(entity.SubjectType), GranteeUserID: entity.GranteeUserID, GranteeSpaceID: entity.GranteeSpaceID, AccessRevision: entity.AccessRevision, CreatedAt: entity.CreatedAt, RevokedAt: entity.RevokedAt}
}

func searchProjectionToRecord(entity *domain.SearchProjection) *searchProjectionRecord {
	return &searchProjectionRecord{DocumentID: entity.DocumentID, VersionID: entity.VersionID, OwnerID: entity.OwnerID, OwnerSpaceID: entity.OwnerSpaceID, AuthenticatedPublic: entity.AuthenticatedPublic, Title: entity.Title, Summary: entity.Summary, SearchText: entity.SearchText, ActivationRevision: entity.ActivationRevision, AccessRevision: entity.AccessRevision, LifecycleRevision: entity.LifecycleRevision, UpdatedAt: entity.UpdatedAt}
}

func indexDeliveryEventToRecord(entity *domain.IndexDeliveryEvent) (*indexDeliveryEventRecord, error) {
	spaceIDs := entity.GrantedSpaceIDs
	if spaceIDs == nil {
		spaceIDs = []string{}
	}
	grantedSpaceIDs, err := json.Marshal(spaceIDs)
	if err != nil {
		return nil, fmt.Errorf("encode granted space ids: %w", err)
	}
	var ownerSpaceID *string
	if entity.OwnerSpaceID != "" {
		ownerSpaceID = &entity.OwnerSpaceID
	}
	return &indexDeliveryEventRecord{
		EventID: entity.EventID, DedupeKey: entity.DedupeKey, Source: string(entity.Source), SourceRunID: entity.SourceRunID,
		DocumentID: entity.DocumentID, AggregateRevision: entity.AggregateRevision, EventKind: string(entity.Kind),
		VersionID: entity.VersionID, PreviousVersionID: entity.PreviousVersionID, OwnerSpaceID: ownerSpaceID,
		ActivationRevision: entity.ActivationRevision, AccessRevision: entity.AccessRevision, LifecycleRevision: entity.LifecycleRevision,
		AuthenticatedPublic: entity.AuthenticatedPublic, GrantedSpaceIDsJSON: string(grantedSpaceIDs),
		ContentSHA256: entity.ContentSHA256, IndexProfile: entity.IndexProfile, State: string(entity.State),
		AttemptCount: entity.AttemptCount, AvailableAt: entity.AvailableAt, LeaseOwner: entity.LeaseOwner,
		LeaseToken: entity.LeaseToken, LeaseExpiresAt: entity.LeaseExpiresAt, LastGRPCCode: entity.LastGRPCCode,
		LastError: entity.LastError, LastAttemptAt: entity.LastAttemptAt, DeliveredAt: entity.DeliveredAt,
		CreatedAt: entity.CreatedAt, UpdatedAt: entity.UpdatedAt,
	}, nil
}

func indexDeliveryEventFromRecord(record *indexDeliveryEventRecord) (*domain.IndexDeliveryEvent, error) {
	var grantedSpaceIDs []string
	if err := json.Unmarshal([]byte(record.GrantedSpaceIDsJSON), &grantedSpaceIDs); err != nil {
		return nil, fmt.Errorf("decode granted space ids: %w", err)
	}
	ownerSpaceID := ""
	if record.OwnerSpaceID != nil {
		ownerSpaceID = *record.OwnerSpaceID
	}
	return &domain.IndexDeliveryEvent{
		EventID: record.EventID, DedupeKey: record.DedupeKey, Source: domain.IndexDeliverySource(record.Source), SourceRunID: record.SourceRunID,
		DocumentID: record.DocumentID, AggregateRevision: record.AggregateRevision, Kind: domain.IndexDeliveryKind(record.EventKind),
		VersionID: record.VersionID, PreviousVersionID: record.PreviousVersionID, OwnerSpaceID: ownerSpaceID,
		ActivationRevision: record.ActivationRevision, AccessRevision: record.AccessRevision, LifecycleRevision: record.LifecycleRevision,
		AuthenticatedPublic: record.AuthenticatedPublic, GrantedSpaceIDs: grantedSpaceIDs, ContentSHA256: record.ContentSHA256,
		IndexProfile: record.IndexProfile, State: domain.IndexDeliveryState(record.State), AttemptCount: record.AttemptCount,
		AvailableAt: record.AvailableAt, LeaseOwner: record.LeaseOwner, LeaseToken: record.LeaseToken,
		LeaseExpiresAt: record.LeaseExpiresAt, LastGRPCCode: record.LastGRPCCode, LastError: record.LastError,
		LastAttemptAt: record.LastAttemptAt, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt, DeliveredAt: record.DeliveredAt,
	}, nil
}
