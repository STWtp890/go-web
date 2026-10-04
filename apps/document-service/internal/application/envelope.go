package application

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"document-service/internal/domain"

	documentv1 "packages/gen/document/v1"
	"packages/serviceauth"
)

// This file is the only place protocol messages and domain values are converted.
// Business decisions are never made while converting: a status maps to a status,
// a revision to a revision.

// formatTime renders a timestamp in the canonical wire form used by every
// timestamp field of the contract.
func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

// contentDigest is the stored content hash. The database check constraint
// requires exactly 64 lowercase hexadecimal characters.
func contentDigest(content string) string {
	digest := sha256.Sum256([]byte(content))
	return hex.EncodeToString(digest[:])
}

func lifecycleProto(status string) documentv1.LifecycleStatus {
	switch status {
	case domain.LifecycleActive:
		return documentv1.LifecycleStatus_LIFECYCLE_STATUS_ACTIVE
	case domain.LifecycleArchived:
		return documentv1.LifecycleStatus_LIFECYCLE_STATUS_ARCHIVED
	case domain.LifecycleTrashed:
		return documentv1.LifecycleStatus_LIFECYCLE_STATUS_TRASHED
	default:
		return documentv1.LifecycleStatus_LIFECYCLE_STATUS_UNSPECIFIED
	}
}

// lifecycleFromProto maps the request enum onto the stored status. An unspecified
// filter is not an error: it means "do not filter".
func lifecycleFromProto(status documentv1.LifecycleStatus) (string, error) {
	switch status {
	case documentv1.LifecycleStatus_LIFECYCLE_STATUS_UNSPECIFIED:
		return "", nil
	case documentv1.LifecycleStatus_LIFECYCLE_STATUS_ACTIVE:
		return domain.LifecycleActive, nil
	case documentv1.LifecycleStatus_LIFECYCLE_STATUS_ARCHIVED:
		return domain.LifecycleArchived, nil
	case documentv1.LifecycleStatus_LIFECYCLE_STATUS_TRASHED:
		return domain.LifecycleTrashed, nil
	default:
		return "", fmt.Errorf("%w: unknown lifecycle status %d", domain.ErrInvalidInput, int32(status))
	}
}

func publicationProto(status string) documentv1.PublicationStatus {
	switch status {
	case domain.PublicationDraft:
		return documentv1.PublicationStatus_PUBLICATION_STATUS_DRAFT
	case domain.PublicationPublished:
		return documentv1.PublicationStatus_PUBLICATION_STATUS_PUBLISHED
	case domain.PublicationSuperseded:
		return documentv1.PublicationStatus_PUBLICATION_STATUS_SUPERSEDED
	case domain.PublicationWithdrawn:
		return documentv1.PublicationStatus_PUBLICATION_STATUS_WITHDRAWN
	default:
		return documentv1.PublicationStatus_PUBLICATION_STATUS_UNSPECIFIED
	}
}

func eventKindProto(kind string) documentv1.DocumentEventKind {
	switch kind {
	case domain.EventKindUpsert:
		return documentv1.DocumentEventKind_DOCUMENT_EVENT_KIND_UPSERT
	case domain.EventKindDelete:
		return documentv1.DocumentEventKind_DOCUMENT_EVENT_KIND_DELETE
	default:
		return documentv1.DocumentEventKind_DOCUMENT_EVENT_KIND_UNSPECIFIED
	}
}

func spaceTypeProto(spaceType string) documentv1.SpaceType {
	switch spaceType {
	case domain.SpaceTypePrivate:
		return documentv1.SpaceType_SPACE_TYPE_PRIVATE
	case domain.SpaceTypeTeam:
		return documentv1.SpaceType_SPACE_TYPE_TEAM
	default:
		return documentv1.SpaceType_SPACE_TYPE_UNSPECIFIED
	}
}

func memberRoleProto(role string) documentv1.MemberRole {
	switch role {
	case domain.RoleOwner:
		return documentv1.MemberRole_MEMBER_ROLE_OWNER
	case domain.RoleAdmin:
		return documentv1.MemberRole_MEMBER_ROLE_ADMIN
	case domain.RoleMember:
		return documentv1.MemberRole_MEMBER_ROLE_MEMBER
	default:
		return documentv1.MemberRole_MEMBER_ROLE_UNSPECIFIED
	}
}

func memberRoleFromProto(role documentv1.MemberRole) (string, error) {
	switch role {
	case documentv1.MemberRole_MEMBER_ROLE_UNSPECIFIED:
		return domain.RoleMember, nil
	case documentv1.MemberRole_MEMBER_ROLE_OWNER:
		return domain.RoleOwner, nil
	case documentv1.MemberRole_MEMBER_ROLE_ADMIN:
		return domain.RoleAdmin, nil
	case documentv1.MemberRole_MEMBER_ROLE_MEMBER:
		return domain.RoleMember, nil
	default:
		return "", fmt.Errorf("%w: unknown member role %d", domain.ErrInvalidInput, int32(role))
	}
}

func subjectTypeProto(subjectType string) documentv1.SubjectType {
	switch subjectType {
	case domain.SubjectTypeGroup:
		return documentv1.SubjectType_SUBJECT_TYPE_GROUP
	case domain.SubjectTypeUser:
		return documentv1.SubjectType_SUBJECT_TYPE_USER
	default:
		return documentv1.SubjectType_SUBJECT_TYPE_UNSPECIFIED
	}
}

func deniedReasonProto(reason domain.DeniedReason) documentv1.DeniedReason {
	switch reason {
	case domain.DeniedSubjectUnknown:
		return documentv1.DeniedReason_DENIED_REASON_SUBJECT_UNKNOWN
	case domain.DeniedSubjectRevoked:
		return documentv1.DeniedReason_DENIED_REASON_SUBJECT_REVOKED
	case domain.DeniedSubjectInactive:
		return documentv1.DeniedReason_DENIED_REASON_SUBJECT_INACTIVE
	case domain.DeniedGroupUnbound:
		return documentv1.DeniedReason_DENIED_REASON_GROUP_UNBOUND
	case domain.DeniedGroupBindingRevoked:
		return documentv1.DeniedReason_DENIED_REASON_GROUP_BINDING_REVOKED
	case domain.DeniedNotSpaceMember:
		return documentv1.DeniedReason_DENIED_REASON_NOT_SPACE_MEMBER
	case domain.DeniedSessionContextInvalid:
		return documentv1.DeniedReason_DENIED_REASON_SESSION_CONTEXT_INVALID
	default:
		return documentv1.DeniedReason_DENIED_REASON_UNSPECIFIED
	}
}

// conversationFromRequest maps the request body session context.
//
// The group id is kept even for a private conversation so Conversation.Validate can
// reject the contradiction. Dropping it here would silently turn "private session
// with a group" into a plain private session, which is exactly the kind of quiet
// reinterpretation of caller input the contract forbids.
func conversationFromRequest(message *documentv1.ConversationContext) (domain.Conversation, error) {
	if message == nil {
		return domain.Conversation{}, fmt.Errorf("%w: a conversation context is required", domain.ErrInvalidInput)
	}
	switch message.GetKind() {
	case documentv1.ConversationKind_CONVERSATION_KIND_PRIVATE:
		return domain.Conversation{
			Kind:            domain.ConversationPrivate,
			ExternalGroupID: message.GetExternalGroupId(),
		}, nil
	case documentv1.ConversationKind_CONVERSATION_KIND_GROUP:
		return domain.Conversation{
			Kind:            domain.ConversationGroup,
			ExternalGroupID: message.GetExternalGroupId(),
		}, nil
	default:
		return domain.Conversation{}, fmt.Errorf("%w: unknown conversation kind %d", domain.ErrInvalidInput, int32(message.GetKind()))
	}
}

// conversationFromAssertion maps the session context the trusted entry point
// proved. Like the request mapping it keeps every field it was given, so the
// validation below can refuse a context that contradicts itself.
func conversationFromAssertion(conversation *serviceauth.Conversation) (domain.Conversation, bool, error) {
	if conversation == nil {
		return domain.Conversation{}, false, nil
	}
	switch conversation.Kind {
	case serviceauth.ConversationPrivate:
		return domain.Conversation{
			Kind:            domain.ConversationPrivate,
			ExternalGroupID: conversation.ExternalGroupID,
		}, true, nil
	case serviceauth.ConversationGroup:
		return domain.Conversation{
			Kind:            domain.ConversationGroup,
			ExternalGroupID: conversation.ExternalGroupID,
		}, true, nil
	default:
		return domain.Conversation{}, false, fmt.Errorf("%w: unknown conversation kind %q", domain.ErrInvalidInput, conversation.Kind)
	}
}

func sourceProto(source domain.Source) *documentv1.DocumentSource {
	return &documentv1.DocumentSource{
		Origin:         source.Origin,
		BotId:          source.BotID,
		ConversationId: source.ConversationID,
		SourceRecordId: source.SourceRecordID,
	}
}

// sourceFromProto validates an origin trace. A source record id without an origin
// is refused: the record would be unattributable and could never be deduplicated.
func sourceFromProto(source *documentv1.DocumentSource) (*domain.Source, error) {
	if source == nil {
		return nil, nil
	}
	origin := strings.TrimSpace(source.GetOrigin())
	botID := strings.TrimSpace(source.GetBotId())
	conversationID := strings.TrimSpace(source.GetConversationId())
	recordID := strings.TrimSpace(source.GetSourceRecordId())
	if origin == "" && botID == "" && conversationID == "" && recordID == "" {
		return nil, nil
	}
	if origin == "" {
		return nil, fmt.Errorf("%w: a document source requires an origin", domain.ErrInvalidInput)
	}
	if len(origin) > 32 {
		return nil, fmt.Errorf("%w: document source origin is too long", domain.ErrInvalidInput)
	}
	if botID != "" && !domain.ValidExternalID(botID) {
		return nil, fmt.Errorf("%w: document source has an unusable bot id", domain.ErrInvalidInput)
	}
	if len(conversationID) > 192 || len(recordID) > 192 {
		return nil, fmt.Errorf("%w: document source identifiers are too long", domain.ErrInvalidInput)
	}
	return &domain.Source{
		Origin:         origin,
		BotID:          botID,
		ConversationID: conversationID,
		SourceRecordID: recordID,
	}, nil
}

func summaryProto(summary domain.Summary) *documentv1.DocumentSummary {
	return &documentv1.DocumentSummary{
		DocumentId:          summary.DocumentID,
		OwnerSubjectKey:     summary.OwnerSubjectKey,
		OwnerSpaceId:        summary.OwnerSpaceID,
		LifecycleStatus:     lifecycleProto(summary.LifecycleStatus),
		ActiveVersionId:     summary.ActiveVersionID,
		Title:               summary.Title,
		PublicationStatus:   publicationProto(summary.PublicationStatus),
		AggregateRevision:   uint64(summary.AggregateRevision),
		CreatedAt:           formatTime(summary.CreatedAt),
		UpdatedAt:           formatTime(summary.UpdatedAt),
		AuthenticatedPublic: summary.AuthenticatedPublic,
	}
}

// documentSummary builds the summary of a document head together with the version
// the head points at and the access policy that governs it. The version may be nil
// for a document whose only published version was withdrawn. The public flag is
// taken from the policy row, never inferred from the publication status.
func documentSummary(document domain.Document, version *domain.Version, policy domain.AccessPolicy) *documentv1.DocumentSummary {
	summary := domain.Summary{
		DocumentID:          document.DocumentID,
		OwnerSubjectKey:     document.OwnerSubjectKey,
		OwnerSpaceID:        document.OwnerSpaceID,
		LifecycleStatus:     document.LifecycleStatus,
		ActiveVersionID:     document.ActiveVersionID,
		AggregateRevision:   document.AggregateRevision,
		AuthenticatedPublic: policy.AuthenticatedPublic,
		CreatedAt:           document.CreatedAt,
		UpdatedAt:           document.UpdatedAt,
	}
	if version != nil {
		summary.Title = version.Title
		summary.PublicationStatus = version.PublicationStatus
	}
	return summaryProto(summary)
}

func versionProto(version domain.Version) *documentv1.DocumentVersion {
	return &documentv1.DocumentVersion{
		VersionId:           version.VersionID,
		DocumentId:          version.DocumentID,
		Revision:            uint64(version.Revision),
		PublicationStatus:   publicationProto(version.PublicationStatus),
		Title:               version.Title,
		Summary:             version.Summary,
		Content:             version.Content,
		ContentFormat:       version.ContentFormat,
		ContentSha256:       version.ContentSHA256,
		CreatedBySubjectKey: version.CreatedBySubjectKey,
		CreatedAt:           formatTime(version.CreatedAt),
	}
}

func spaceProto(space domain.Space) *documentv1.KnowledgeSpace {
	return &documentv1.KnowledgeSpace{
		SpaceId:         space.SpaceID,
		OwnerSubjectKey: space.OwnerSubjectKey,
		SpaceType:       spaceTypeProto(space.SpaceType),
		Name:            space.Name,
		CreatedAt:       formatTime(space.CreatedAt),
		UpdatedAt:       formatTime(space.UpdatedAt),
	}
}

func memberProto(member domain.Member) *documentv1.SpaceMember {
	proto := &documentv1.SpaceMember{
		SpaceId:    member.SpaceID,
		SubjectKey: member.SubjectKey,
		MemberRole: memberRoleProto(member.MemberRole),
		CreatedAt:  formatTime(member.CreatedAt),
	}
	if member.RevokedAt != nil {
		proto.RevokedAt = formatTime(*member.RevokedAt)
	}
	return proto
}

func bindingProto(binding domain.Binding) *documentv1.GroupSpaceBinding {
	proto := &documentv1.GroupSpaceBinding{
		BindingId:       binding.BindingID,
		Channel:         binding.Channel,
		BotId:           binding.BotID,
		ExternalGroupId: binding.ExternalGroupID,
		SpaceId:         binding.SpaceID,
		Actor:           binding.Actor,
		Source:          binding.Source,
		Reason:          binding.Reason,
		CreatedAt:       formatTime(binding.CreatedAt),
	}
	if binding.RevokedAt != nil {
		proto.RevokedAt = formatTime(*binding.RevokedAt)
	}
	return proto
}

func subjectProto(subject domain.Subject) *documentv1.AccessSubject {
	return &documentv1.AccessSubject{
		SubjectKey:  subject.SubjectKey,
		SubjectType: subjectTypeProto(subject.SubjectType),
		Origin:      subject.Origin,
		DisplayName: subject.DisplayName,
		Active:      subject.Active,
		CreatedAt:   formatTime(subject.CreatedAt),
		UpdatedAt:   formatTime(subject.UpdatedAt),
	}
}

// envelopeInput is everything one Outbox event carries. It is assembled after the
// business mutation has been applied, so the event describes the committed state.
type envelopeInput struct {
	Sequence        int64
	EventID         string
	Kind            string
	Document        domain.Document
	Version         *domain.Version
	Policy          domain.AccessPolicy
	GrantedSpaceIDs []string
	Source          *domain.Source
	OccurredAt      time.Time
}

// buildEnvelope renders the document change event. A delete event carries the
// fencing revisions and the version it retired, but no content: the consumer must
// remove the document, not re-index it.
func buildEnvelope(input envelopeInput) *documentv1.DocumentEventEnvelope {
	envelope := &documentv1.DocumentEventEnvelope{
		Sequence:            input.Sequence,
		EventId:             input.EventID,
		Kind:                eventKindProto(input.Kind),
		DocumentId:          input.Document.DocumentID,
		AggregateRevision:   uint64(input.Document.AggregateRevision),
		ActivationRevision:  uint64(input.Document.ActivationRevision),
		AccessRevision:      uint64(input.Document.AccessRevision),
		LifecycleRevision:   uint64(input.Document.LifecycleRevision),
		LifecycleStatus:     lifecycleProto(input.Document.LifecycleStatus),
		OwnerSubjectKey:     input.Document.OwnerSubjectKey,
		OwnerSpaceId:        input.Document.OwnerSpaceID,
		AuthenticatedPublic: input.Policy.AuthenticatedPublic,
		AllowedSpaceIds:     serviceauth.CanonicalIDs(input.GrantedSpaceIDs),
		IndexProfile:        domain.DefaultIndexProfile,
		OccurredAt:          formatTime(input.OccurredAt),
		// The document's creation instant, not the event instant: a consumer can
		// present a real creation time without calling back into this service.
		CreatedAt: formatTime(input.Document.CreatedAt),
	}
	if input.Version != nil {
		envelope.VersionId = input.Version.VersionID
		envelope.PublicationStatus = publicationProto(input.Version.PublicationStatus)
		if input.Kind == domain.EventKindUpsert {
			envelope.Title = input.Version.Title
			envelope.Summary = input.Version.Summary
			envelope.Content = input.Version.Content
			envelope.ContentFormat = input.Version.ContentFormat
			envelope.ContentSha256 = input.Version.ContentSHA256
		}
	}
	if input.Source != nil {
		envelope.Source = sourceProto(*input.Source)
	}
	return envelope
}

// dedupeKey is the Outbox uniqueness key. One command writes at most one event per
// document revision, so a retried command cannot append a second row describing
// the same state.
func dedupeKey(documentID, kind string, aggregateRevision int64) string {
	return fmt.Sprintf("%s:%s:%d", kind, documentID, aggregateRevision)
}
