// Package domain 定义文档领域的持久化无关模型。
package domain

import (
	"time"
)

type SpaceType string

const (
	SpaceTypePrivate SpaceType = "private"
	SpaceTypeTeam    SpaceType = "team"
)

type MemberRole string

const (
	MemberRoleOwner  MemberRole = "owner"
	MemberRoleAdmin  MemberRole = "admin"
	MemberRoleMember MemberRole = "member"
)

type LifecycleStatus string

const (
	LifecycleActive   LifecycleStatus = "active"
	LifecycleArchived LifecycleStatus = "archived"
	LifecycleTrashed  LifecycleStatus = "trashed"
)

type PublicationStatus string

const (
	PublicationDraft      PublicationStatus = "draft"
	PublicationPublished  PublicationStatus = "published"
	PublicationSuperseded PublicationStatus = "superseded"
	PublicationWithdrawn  PublicationStatus = "withdrawn"
)

type GrantSubjectType string

const (
	GrantSubjectUser  GrantSubjectType = "user"
	GrantSubjectSpace GrantSubjectType = "space"
)

type KnowledgeSpace struct {
	SpaceID   string
	OwnerID   int64
	SpaceType SpaceType
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type SpaceMember struct {
	SpaceID    string
	UserID     int64
	MemberRole MemberRole
	CreatedAt  time.Time
	UpdatedAt  time.Time
	RevokedAt  *time.Time
}

type Document struct {
	DocumentID         string
	OwnerID            int64
	OwnerSpaceID       string
	LifecycleStatus    LifecycleStatus
	ActiveVersionID    *string
	ActivationRevision int64
	AccessRevision     int64
	LifecycleRevision  int64
	AggregateRevision  int64
	CreatedAt          time.Time
	UpdatedAt          time.Time
	TrashedAt          *time.Time
}

type DocumentVersion struct {
	VersionID         string
	DocumentID        string
	Revision          int64
	PublicationStatus PublicationStatus
	Title             string
	Summary           string
	Content           string
	ContentFormat     string
	ContentSHA256     string
	CreatedBy         int64
	CreatedAt         time.Time
}

type AccessPolicy struct {
	DocumentID          string
	AuthenticatedPublic bool
	AccessRevision      int64
	UpdatedAt           time.Time
}
type DocumentGrant struct {
	GrantID        string
	DocumentID     string
	SubjectType    GrantSubjectType
	GranteeUserID  *int64
	GranteeSpaceID *string
	AccessRevision int64
	CreatedAt      time.Time
	RevokedAt      *time.Time
}

type SearchProjection struct {
	DocumentID          string
	VersionID           string
	OwnerID             int64
	OwnerSpaceID        string
	AuthenticatedPublic bool
	Title               string
	Summary             string
	SearchText          string
	ActivationRevision  int64
	AccessRevision      int64
	LifecycleRevision   int64
	UpdatedAt           time.Time
}
