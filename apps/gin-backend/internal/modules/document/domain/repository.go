package domain

import (
	"context"
	"errors"
	"time"
)

var ErrNotFound = errors.New("document repository: not found")

// Repository 提供文档聚合的持久化原语。跨表原子性由 InTransaction 调用方显式组织。
type Repository interface {
	InTransaction(context.Context, func(Repository) error) error
	EnsurePrivateSpace(context.Context, *KnowledgeSpace) (bool, error)
	CreateKnowledgeSpace(context.Context, *KnowledgeSpace) error
	CreateSpaceMember(context.Context, *SpaceMember) error
	CreateDocument(context.Context, *Document) error
	CreateDocumentVersion(context.Context, *DocumentVersion) error
	UpdateVersionPublicationStatus(context.Context, string, PublicationStatus) error
	SetActiveVersion(context.Context, string, string, int64, int64) error
	SetAccessRevision(context.Context, string, int64, int64) error
	SetLifecycleState(context.Context, string, LifecycleStatus, int64, int64, *time.Time) error
	PutAccessPolicy(context.Context, *AccessPolicy) error
	CreateGrant(context.Context, *DocumentGrant) error
	PutSearchProjection(context.Context, *SearchProjection) error
	DeleteSearchProjection(context.Context, string) error
	GetDocument(context.Context, string) (*Document, error)
	GetAccessPolicy(context.Context, string) (*AccessPolicy, error)
	GetLatestDocumentVersion(context.Context, string) (*DocumentVersion, error)
	ListDocumentVersions(context.Context, string) ([]DocumentVersion, error)
	LockDocument(context.Context, string) (*Document, error)
}
