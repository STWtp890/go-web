package postgresql

import (
	"time"
)

type knowledgeSpaceRecord struct {
	SpaceID   string    `gorm:"column:space_id;type:uuid;primaryKey"`
	OwnerID   int64     `gorm:"column:owner_id;not null"`
	SpaceType string    `gorm:"column:space_type;size:16;not null"`
	Name      string    `gorm:"column:name;size:128;not null"`
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt time.Time `gorm:"column:updated_at;autoCreateTime;autoUpdateTime"`
}

func (*knowledgeSpaceRecord) TableName() string { return "knowledge_spaces" }

type spaceMemberRecord struct {
	SpaceID    string     `gorm:"column:space_id;type:uuid;primaryKey"`
	UserID     int64      `gorm:"column:user_id;primaryKey"`
	MemberRole string     `gorm:"column:member_role;size:16;not null"`
	CreatedAt  time.Time  `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt  time.Time  `gorm:"column:updated_at;autoCreateTime;autoUpdateTime"`
	RevokedAt  *time.Time `gorm:"column:revoked_at"`
}

func (*spaceMemberRecord) TableName() string { return "space_members" }

type documentRecord struct {
	DocumentID         string     `gorm:"column:document_id;type:uuid;primaryKey"`
	OwnerID            int64      `gorm:"column:owner_id;not null"`
	OwnerSpaceID       string     `gorm:"column:owner_space_id;type:uuid;not null"`
	LifecycleStatus    string     `gorm:"column:lifecycle_status;size:16;not null"`
	ActiveVersionID    *string    `gorm:"column:active_version_id;type:uuid"`
	ActivationRevision int64      `gorm:"column:activation_revision;not null"`
	AccessRevision     int64      `gorm:"column:access_revision;not null"`
	LifecycleRevision  int64      `gorm:"column:lifecycle_revision;not null"`
	AggregateRevision  int64      `gorm:"column:aggregate_revision;not null"`
	CreatedAt          time.Time  `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt          time.Time  `gorm:"column:updated_at;autoCreateTime;autoUpdateTime"`
	TrashedAt          *time.Time `gorm:"column:trashed_at"`
}

func (*documentRecord) TableName() string { return "documents" }

type documentVersionRecord struct {
	VersionID         string    `gorm:"column:version_id;type:uuid;primaryKey"`
	DocumentID        string    `gorm:"column:document_id;type:uuid;not null"`
	Revision          int64     `gorm:"column:revision;not null"`
	PublicationStatus string    `gorm:"column:publication_status;size:16;not null"`
	Title             string    `gorm:"column:title;size:255;not null"`
	Summary           string    `gorm:"column:summary;size:512;not null"`
	Content           string    `gorm:"column:content;type:text;not null"`
	ContentFormat     string    `gorm:"column:content_format;size:16;not null"`
	ContentSHA256     string    `gorm:"column:content_sha256;size:64;not null"`
	CreatedBy         int64     `gorm:"column:created_by;not null"`
	CreatedAt         time.Time `gorm:"column:created_at;autoCreateTime"`
}

func (*documentVersionRecord) TableName() string { return "document_versions" }

type accessPolicyRecord struct {
	DocumentID          string    `gorm:"column:document_id;type:uuid;primaryKey"`
	AuthenticatedPublic bool      `gorm:"column:authenticated_public;not null"`
	AccessRevision      int64     `gorm:"column:access_revision;not null"`
	UpdatedAt           time.Time `gorm:"column:updated_at;autoCreateTime;autoUpdateTime"`
}

func (*accessPolicyRecord) TableName() string { return "document_access_policies" }

type documentGrantRecord struct {
	GrantID        string     `gorm:"column:grant_id;type:uuid;primaryKey"`
	DocumentID     string     `gorm:"column:document_id;type:uuid;not null"`
	SubjectType    string     `gorm:"column:subject_type;size:16;not null"`
	GranteeUserID  *int64     `gorm:"column:grantee_user_id"`
	GranteeSpaceID *string    `gorm:"column:grantee_space_id;type:uuid"`
	AccessRevision int64      `gorm:"column:access_revision;not null"`
	CreatedAt      time.Time  `gorm:"column:created_at;autoCreateTime"`
	RevokedAt      *time.Time `gorm:"column:revoked_at"`
}

func (*documentGrantRecord) TableName() string { return "document_grants" }

type searchProjectionRecord struct {
	DocumentID          string    `gorm:"column:document_id;type:uuid;primaryKey"`
	VersionID           string    `gorm:"column:version_id;type:uuid;not null"`
	OwnerID             int64     `gorm:"column:owner_id;not null"`
	OwnerSpaceID        string    `gorm:"column:owner_space_id;type:uuid;not null"`
	AuthenticatedPublic bool      `gorm:"column:authenticated_public;not null"`
	Title               string    `gorm:"column:title;size:255;not null"`
	Summary             string    `gorm:"column:summary;size:512;not null"`
	SearchText          string    `gorm:"column:search_text;type:text;not null"`
	ActivationRevision  int64     `gorm:"column:activation_revision;not null"`
	AccessRevision      int64     `gorm:"column:access_revision;not null"`
	LifecycleRevision   int64     `gorm:"column:lifecycle_revision;not null"`
	UpdatedAt           time.Time `gorm:"column:updated_at;autoCreateTime;autoUpdateTime"`
}

func (*searchProjectionRecord) TableName() string { return "document_search_projection" }
