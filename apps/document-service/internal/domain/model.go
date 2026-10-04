// Package domain holds the document service's business vocabulary: the formal
// document aggregate and its revisions, knowledge spaces, membership, group to
// space bindings, the resource scope resolution and the sentinel errors the use
// cases return.
//
// It deliberately has no persistence, transport or configuration dependency. The
// rules that decide "who may do what to which revision", the label invariants of
// a resolved resource envelope and the denial vocabulary are therefore testable
// without a database, and the SQL layer can only observe them, never redefine
// them.
package domain

import "time"

// Lifecycle statuses of a formal document. They mirror
// document_service.documents.lifecycle_status.
const (
	LifecycleActive   = "active"
	LifecycleArchived = "archived"
	LifecycleTrashed  = "trashed"
)

// Publication statuses of one document version. They mirror
// document_service.document_versions.publication_status.
const (
	PublicationDraft      = "draft"
	PublicationPublished  = "published"
	PublicationSuperseded = "superseded"
	PublicationWithdrawn  = "withdrawn"
)

// Knowledge space types.
const (
	SpaceTypePrivate = "private"
	SpaceTypeTeam    = "team"
)

// Space member roles.
const (
	RoleOwner  = "owner"
	RoleAdmin  = "admin"
	RoleMember = "member"
)

// Access subject shape. A subject key is "<origin>:<type>:<local id>"; the
// registry is owned by this service and never references another service's user
// table.
const (
	SubjectTypeUser  = "user"
	SubjectTypeGroup = "group"

	OriginWeb = "web"
	OriginQQ  = "qq"
)

// ChannelQQ is the only channel the group binding model has today. The column is
// present so a second channel is a row, not a model change.
const ChannelQQ = "qq"

// Outbox event kinds. The document contract has exactly two.
const (
	EventKindUpsert = "upsert"
	EventKindDelete = "delete"
)

// Content formats accepted by document_versions.content_format.
const (
	ContentFormatMarkdown = "markdown"
	ContentFormatDoc      = "doc"
	ContentFormatDocx     = "docx"
)

// DefaultIndexProfile is written into every Outbox event so the consumer knows
// which chunking profile produced the content.
const DefaultIndexProfile = "markdown-v1"

// Document is the formal document aggregate head. The four revisions are the
// fencing values the search index orders events by; they must only ever move
// forward.
type Document struct {
	DocumentID         string
	OwnerSubjectKey    string
	OwnerSpaceID       string
	LifecycleStatus    string
	ActiveVersionID    string
	ActivationRevision int64
	AccessRevision     int64
	LifecycleRevision  int64
	AggregateRevision  int64
	// CreateRequestID is the caller supplied idempotency key of the creation
	// command. It is empty when the caller supplied none.
	CreateRequestID string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	TrashedAt       *time.Time
}

// SaveRequest is one row of the durable SaveDocument idempotency ledger: the
// caller supplied request id, the version the first attempt produced and the
// fingerprint of the payload it was committed with.
//
// The ledger is append-only and keyed by (DocumentID, RequestID), so a retry is
// recognized whenever it arrives - not only while it is the most recent save. It
// commits in the same transaction as the version it names, so a rolled back save
// can never leave an "already handled" record behind.
type SaveRequest struct {
	DocumentID string
	RequestID  string
	// VersionID is the version the first attempt created. It may since have been
	// superseded: it is the correlation handle of the original attempt, not a
	// claim about the current active version.
	VersionID string
	// PayloadFingerprint is the digest of the business inputs the request was
	// first committed with. The same request id carrying a different payload is a
	// caller defect, not a replay.
	PayloadFingerprint string
	CreatedAt          time.Time
}

// Summary is the list projection of a document: the aggregate head plus the
// title and publication status of its active version (or of the newest version
// when nothing is active).
//
// AuthenticatedPublic is the access policy's flag, read from the policy row. It
// is deliberately not derived from the publication status: publication says which
// version is active, the policy says who may read it. Carrying it here is what
// keeps a list summary and a detail read of the same document from disagreeing
// about visibility.
type Summary struct {
	DocumentID          string
	OwnerSubjectKey     string
	OwnerSpaceID        string
	LifecycleStatus     string
	ActiveVersionID     string
	Title               string
	PublicationStatus   string
	AggregateRevision   int64
	AuthenticatedPublic bool
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// Version is one immutable content revision of a document. Only its publication
// status may change after it is written; the database enforces the same rule.
type Version struct {
	VersionID           string
	DocumentID          string
	Revision            int64
	PublicationStatus   string
	Title               string
	Summary             string
	Content             string
	ContentFormat       string
	ContentSHA256       string
	CreatedBySubjectKey string
	CreatedAt           time.Time
}

// AccessPolicy is the document level access flag. It carries its own revision so
// an access change is fenced independently from an activation change.
type AccessPolicy struct {
	DocumentID          string
	AuthenticatedPublic bool
	AccessRevision      int64
	UpdatedAt           time.Time
}

// Source traces content promoted from a raw record (for example a QQ file or
// message). The source record id gives idempotent submission; it never replaces
// the document id and never makes raw content a formal document by itself.
type Source struct {
	Origin         string
	BotID          string
	ConversationID string
	SourceRecordID string
}

// IsEmpty reports whether the trace carries no source identity at all.
func (source Source) IsEmpty() bool {
	return source.Origin == "" && source.BotID == "" && source.ConversationID == "" && source.SourceRecordID == ""
}

// Space is a knowledge space: private (owned by exactly one subject) or team.
type Space struct {
	SpaceID         string
	OwnerSubjectKey string
	SpaceType       string
	Name            string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Member is one space membership row. Revoked rows are kept for audit; an active
// row has RevokedAt == nil.
type Member struct {
	SpaceID    string
	SubjectKey string
	MemberRole string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	RevokedAt  *time.Time
}

// Active reports whether the membership currently grants access.
func (member Member) Active() bool { return member.RevokedAt == nil }

// Binding is one QQ group to space binding row. Rebinding closes the old row and
// appends a new one, so the history stays auditable.
type Binding struct {
	BindingID       string
	Channel         string
	BotID           string
	ExternalGroupID string
	SpaceID         string
	Actor           string
	Source          string
	Reason          string
	CreatedAt       time.Time
	RevokedAt       *time.Time
}

// Active reports whether the binding currently maps the group to its space.
func (binding Binding) Active() bool { return binding.RevokedAt == nil }

// Subject is one registered resource access subject. Registration records that a
// trusted entry point declared the subject; it grants nothing.
type Subject struct {
	SubjectKey  string
	SubjectType string
	Origin      string
	DisplayName string
	Active      bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Event is one row of the document change Outbox. The table is append-only.
type Event struct {
	Sequence          int64
	EventID           string
	DocumentID        string
	EventKind         string
	AggregateRevision int64
	Payload           []byte
	OccurredAt        time.Time
}

// AuditEvent is one append-only audit row. Every subject, space, membership and
// binding mutation commits its audit row in the same transaction.
type AuditEvent struct {
	EventID        string
	SubjectType    string
	Action         string
	SubjectKey     string
	Channel        string
	BotID          string
	ExternalID     string
	SpaceID        string
	PreviousTarget string
	NewTarget      string
	Actor          string
	Source         string
	Reason         string
	RequestID      string
	OccurredAt     time.Time
}

// Audit entity and action vocabulary. The set is fixed by the audit table's
// check constraints: the ledger records subjects, spaces, memberships and group
// bindings. Document mutations are carried by the Outbox instead of being
// duplicated here.
const (
	AuditSubjectTypeAccessSubject     = "access_subject"
	AuditSubjectTypeSpace             = "space"
	AuditSubjectTypeSpaceMember       = "space_member"
	AuditSubjectTypeGroupSpaceBinding = "group_space_binding"

	AuditActionRegister = "register"
	AuditActionCreate   = "create"
	AuditActionGrant    = "grant"
	AuditActionRevoke   = "revoke"
	AuditActionBind     = "bind"
	AuditActionRebind   = "rebind"
)
