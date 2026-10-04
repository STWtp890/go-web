package application

// This file holds the two raw QQ record models' shared vocabulary *and nothing
// else*. Messages and files have separate tables, separate lifecycle helpers
// (messages.go / files.go) and separate index collections; the only thing they
// share here is the kind/status vocabulary and the event-input validation that
// turns a transport-neutral request into a checked event.
//
// A lifecycle helper that assumed "the other" model exists would be reachable
// from both code paths, which is how "recall a message" would accidentally
// become reachable from a file. There is deliberately no such helper.

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	qqsearchv1 "packages/gen/qqsearch/v1"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Storage objects. Every statement in this service names its table through one
// of these constants so a reviewer can see, at a glance, which model a query
// belongs to. Messages and files never share a table.
const (
	messageTable     = "qq_search.qq_messages"
	fileTable        = "qq_search.qq_files"
	ledgerTable      = "qq_search.qq_applied_events"
	consumerTable    = "qq_search.consumer_state"
	rebuildRunTable  = "qq_search.rebuild_runs"
	collectionsTable = "qq_search.index_collections"
)

// consumerStream is the cursor row this service advances. It matches the row the
// schema baseline seeds.
const consumerStream = "qq-source-events"

// Fixed vocabulary of the wire contract.
const (
	channelQQ           = "qq"
	conversationPrivate = "private"
	conversationGroup   = "group"
)

// Event kinds. The set is exactly the four kinds of qqsource.v1: there is no
// delete kind, because recall is an append-only fact and deleting a raw record
// is not something this service may do.
const (
	EventMessageUpsert   = "message_upsert"
	EventFileUpsert      = "file_upsert"
	EventMessageRecalled = "message_recalled"
	EventFileRecalled    = "file_recalled"
)

// AdvertisedEventKinds is the complete set of event kinds this service applies.
// Anything else is INVALID_ARGUMENT; in particular no "deleted" kind exists.
var AdvertisedEventKinds = []string{
	EventMessageUpsert,
	EventFileUpsert,
	EventMessageRecalled,
	EventFileRecalled,
}

// Outcome reasons returned on the wire.
const (
	ReasonApplied           = "applied"
	ReasonDuplicate         = "duplicate"
	ReasonDuplicateRevision = "duplicate_revision"
	ReasonStaleRevision     = "stale_revision"
)

// RecordKind identifies which of the two independent raw QQ corpora a record
// belongs to. It is not a "document type": the two corpora differ in their
// columns, their lifecycle transitions and their index collection.
type RecordKind string

const (
	// RecordKindMessage is a raw QQ chat message.
	RecordKindMessage RecordKind = "message"
	// RecordKindFile is a raw QQ file.
	RecordKindFile RecordKind = "file"
)

// RecordStatus is the lifecycle state of one raw record.
//
// There is no "stored" value: storing raw QQ content belongs to py-agent. There
// is no reachable "deleted" value either - the schema can represent it, but no
// event kind sets it, so a recalled record stays recalled forever.
type RecordStatus string

const (
	// StatusIndexed means the record is retrievable.
	StatusIndexed RecordStatus = "indexed"
	// StatusRecalled means the source withdrew the record. The row stays.
	StatusRecalled RecordStatus = "recalled"
	// StatusDeleted is representable in storage but is not produced by any
	// event kind: this service never deletes a raw record.
	StatusDeleted RecordStatus = "deleted"
)

// Column limits, taken from the schema baseline. Checking them here turns a
// Postgres 22001 into an explicit INVALID_ARGUMENT that names the field.
const (
	maxRecordIDLen       = 192
	maxBotIDLen          = 64
	maxExternalIDLen     = 64
	maxConversationIDLen = 192
	maxFileNameLen       = 512
	maxMimeTypeLen       = 128
	maxStorageRefLen     = 512
	maxSHA256Len         = 64
)

// maxEventIDLen bounds the producer's opaque event id. The value is only hashed,
// so the limit is about refusing an abusive payload rather than about storage.
const maxEventIDLen = 256

// ledgerNamespace is the fixed namespace of this service's name-based event keys.
// It is a constant rather than configuration so the same producer id always maps
// to the same ledger row on every instance and after every restart.
var ledgerNamespace = [16]byte{
	0x71, 0x71, 0x2d, 0x73, 0x65, 0x61, 0x72, 0x63, 0x68, 0x2d, 0x6c, 0x65, 0x64, 0x67, 0x65, 0x72,
}

// ledgerEventID derives qq_search.qq_applied_events.event_id from py-agent's
// opaque event id.
//
// The ledger column is a uuid, while py-agent's InboundEvent.event_id is a hex
// sha256 over "<conversation_id>:message:<message_id>". Neither side should have to
// move: the producer keeps the id it already derives, and this service derives its
// own idempotency key with the RFC 4122 name-based construction in its version-5
// layout (SHA-256 for the digest). The mapping is deterministic, so a redelivery
// lands on the same ledger row, and injective for practical purposes, so two
// different events never collapse into one.
//
// The key is derived from the producer's *event id*, not from the record identity.
// Deriving it from (channel, bot, conversation, kind, record, revision) would make
// two different events that describe the same revision share one key, and the
// conflicting one would then look like a duplicate delivery instead of the
// ALREADY_EXISTS conflict the contract requires.
func ledgerEventID(producerEventID string) string {
	digest := sha256.New()
	digest.Write(ledgerNamespace[:])
	digest.Write([]byte(producerEventID))
	sum := digest.Sum(nil)

	var raw [16]byte
	copy(raw[:], sum[:16])
	raw[6] = (raw[6] & 0x0f) | 0x50 // RFC 4122 version 5 layout.
	raw[8] = (raw[8] & 0x3f) | 0x80 // RFC 4122 variant.
	return fmt.Sprintf("%x-%x-%x-%x-%x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16])
}

// sourceEvent is one validated qqsource.v1 event, flattened the way the wire
// request carries it.
type sourceEvent struct {
	// producerEventID is the opaque id py-agent assigned to the event. It is
	// neither stored verbatim nor rewritten: the ledger key is derived from it.
	producerEventID string
	// ledgerEventID is the UUID recorded in qq_search.qq_applied_events.event_id.
	ledgerEventID string

	sequence   int64
	eventKind  string
	recordKind RecordKind
	revision   int64

	channel          string
	botID            string
	conversationKind string
	externalUserID   string
	externalGroupID  string
	conversationID   string
	recordID         string

	// Message payload (message_upsert). senderExternalID is the message sender;
	// a file payload never aliases it.
	messageText      string
	senderExternalID string
	sentAt           pgtype.Timestamptz
	platformSequence int64

	// File payload (file_upsert). uploaderUserID comes from the file's own
	// uploader_external_user_id field and is never copied from
	// sender_external_user_id.
	fileName       string
	mimeType       string
	sizeBytes      int64
	contentSHA256  string
	storageRef     string
	uploadedAt     pgtype.Timestamptz
	uploaderUserID string

	// Recall payload (message_recalled / file_recalled).
	recalledAt         pgtype.Timestamptz
	operatorExternalID string

	occurredAt pgtype.Timestamptz
}

// recalls reports whether the event withdraws a record.
func (event *sourceEvent) recalls() bool {
	return event.eventKind == EventMessageRecalled || event.eventKind == EventFileRecalled
}

// recallTimestamp is the effective recall time: the explicit recall instant,
// else the event instant, else "now" at write time.
func (event *sourceEvent) recallTimestamp() pgtype.Timestamptz {
	if event.recalledAt.Valid {
		return event.recalledAt
	}
	return event.occurredAt
}

// querier is the read/write surface a pool and a transaction share. It is
// generic infrastructure: it carries no record semantics, so both models can run
// through it without sharing a lifecycle.
type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// invalidArgument renders a client-visible argument error.
func invalidArgument(format string, args ...any) error {
	return status.Errorf(codes.InvalidArgument, "qq-search: "+format, args...)
}

// internalError renders a server-side failure.
func internalError(format string, args ...any) error {
	return status.Errorf(codes.Internal, "qq-search: "+format, args...)
}

// recordKindForEvent maps an event kind onto the record model it belongs to, and
// reports whether the kind is one this service applies at all.
func recordKindForEvent(eventKind string) (RecordKind, bool) {
	switch eventKind {
	case EventMessageUpsert, EventMessageRecalled:
		return RecordKindMessage, true
	case EventFileUpsert, EventFileRecalled:
		return RecordKindFile, true
	default:
		return "", false
	}
}

// recordKindFromProto maps the lookup selector of GetQQRecordState. UNSPECIFIED
// is rejected rather than defaulted: a record is only addressable inside one of
// the two models, and guessing which one would make a message lookup able to
// answer for a file.
func recordKindFromProto(kind qqsearchv1.QQRecordKind) (RecordKind, error) {
	switch kind {
	case qqsearchv1.QQRecordKind_QQ_RECORD_KIND_MESSAGE:
		return RecordKindMessage, nil
	case qqsearchv1.QQRecordKind_QQ_RECORD_KIND_FILE:
		return RecordKindFile, nil
	default:
		return "", invalidArgument("kind must be QQ_RECORD_KIND_MESSAGE or QQ_RECORD_KIND_FILE, got %s", kind)
	}
}

// protoRecordKind maps a record model onto the wire selector.
func protoRecordKind(kind RecordKind) qqsearchv1.QQRecordKind {
	switch kind {
	case RecordKindMessage:
		return qqsearchv1.QQRecordKind_QQ_RECORD_KIND_MESSAGE
	case RecordKindFile:
		return qqsearchv1.QQRecordKind_QQ_RECORD_KIND_FILE
	default:
		return qqsearchv1.QQRecordKind_QQ_RECORD_KIND_UNSPECIFIED
	}
}

// protoStatus maps a stored lifecycle state onto the wire enum. A state the
// contract does not define is reported as UNSPECIFIED instead of being mapped
// onto a neighbouring value.
func protoStatus(state RecordStatus) qqsearchv1.QQRecordStatus {
	switch state {
	case StatusIndexed:
		return qqsearchv1.QQRecordStatus_QQ_RECORD_STATUS_INDEXED
	case StatusRecalled:
		return qqsearchv1.QQRecordStatus_QQ_RECORD_STATUS_RECALLED
	case StatusDeleted:
		return qqsearchv1.QQRecordStatus_QQ_RECORD_STATUS_DELETED
	default:
		return qqsearchv1.QQRecordStatus_QQ_RECORD_STATUS_UNSPECIFIED
	}
}

// parseSourceEvent validates one IndexQQSourceEvent request and returns the
// checked event. Every rejection is INVALID_ARGUMENT: py-agent is the only
// writer, so a malformed event is a producer bug that must be visible, never
// something to guess around.
func parseSourceEvent(request *qqsearchv1.IndexQQSourceEventRequest) (*sourceEvent, error) {
	if request == nil {
		return nil, invalidArgument("request is required")
	}
	eventKind := strings.TrimSpace(request.GetKind())
	recordKind, known := recordKindForEvent(eventKind)
	if !known {
		return nil, invalidArgument("unknown kind %q: this service applies exactly %s",
			request.GetKind(), strings.Join(AdvertisedEventKinds, ", "))
	}
	event := &sourceEvent{eventKind: eventKind, recordKind: recordKind}

	event.producerEventID = strings.TrimSpace(request.GetEventId())
	if event.producerEventID == "" {
		return nil, invalidArgument("event_id is required")
	}
	if utf8.RuneCountInString(event.producerEventID) > maxEventIDLen {
		return nil, invalidArgument("event_id is %d characters, the service accepts up to %d",
			utf8.RuneCountInString(event.producerEventID), maxEventIDLen)
	}
	// The producer's id stays opaque. py-agent derives a hex sha256 from the
	// conversation and message id, which is not a UUID, and asking it to conform to
	// a storage detail would put this service's column type into its contract. The
	// service derives its own idempotency key instead; see ledgerEventID.
	event.ledgerEventID = ledgerEventID(event.producerEventID)
	if !isUUID(event.ledgerEventID) {
		return nil, internalError("derived ledger event id %q is not a UUID", event.ledgerEventID)
	}
	if request.GetSequence() < 0 {
		return nil, invalidArgument("sequence must not be negative, got %d", request.GetSequence())
	}
	event.sequence = request.GetSequence()

	revision := request.GetRecordRevision()
	if revision == 0 || revision > math.MaxInt64 {
		return nil, invalidArgument("record_revision must be between 1 and %d, got %d", int64(math.MaxInt64), revision)
	}
	event.revision = int64(revision)

	event.channel = strings.ToLower(strings.TrimSpace(request.GetChannel()))
	if event.channel != channelQQ {
		return nil, invalidArgument("channel must be %q, got %q", channelQQ, request.GetChannel())
	}

	event.botID = strings.TrimSpace(request.GetBotId())
	if event.botID == "" {
		return nil, invalidArgument("bot_id is required")
	}
	if err := checkLength("bot_id", event.botID, maxBotIDLen); err != nil {
		return nil, err
	}

	event.recordID = strings.TrimSpace(request.GetRecordId())
	if event.recordID == "" {
		return nil, invalidArgument("record_id is required")
	}
	if err := checkLength("record_id", event.recordID, maxRecordIDLen); err != nil {
		return nil, err
	}

	event.conversationKind = strings.ToLower(strings.TrimSpace(request.GetConversationKind()))
	switch event.conversationKind {
	case conversationPrivate, conversationGroup:
	default:
		return nil, invalidArgument("conversation_kind must be %q or %q, got %q",
			conversationPrivate, conversationGroup, request.GetConversationKind())
	}

	event.conversationID = strings.TrimSpace(request.GetConversationId())
	if event.conversationID == "" {
		return nil, invalidArgument("conversation_id is required")
	}
	if err := checkLength("conversation_id", event.conversationID, maxConversationIDLen); err != nil {
		return nil, err
	}
	event.externalUserID = strings.TrimSpace(request.GetExternalUserId())
	event.externalGroupID = strings.TrimSpace(request.GetExternalGroupId())
	event.operatorExternalID = strings.TrimSpace(request.GetOperatorExternalUserId())
	if err := validateConversation(event); err != nil {
		return nil, err
	}

	occurredAt, err := parseTimestamp("occurred_at", request.GetOccurredAt())
	if err != nil {
		return nil, err
	}
	event.occurredAt = occurredAt

	switch event.eventKind {
	case EventMessageUpsert:
		event.messageText = request.GetMessageText()
		event.senderExternalID = strings.TrimSpace(request.GetSenderExternalUserId())
		if err := checkLength("sender_external_user_id", event.senderExternalID, maxExternalIDLen); err != nil {
			return nil, err
		}
		sentAt, err := parseTimestamp("sent_at", request.GetSentAt())
		if err != nil {
			return nil, err
		}
		event.sentAt = sentAt
		if request.GetPlatformSequence() < 0 {
			return nil, invalidArgument("platform_sequence must not be negative, got %d", request.GetPlatformSequence())
		}
		event.platformSequence = request.GetPlatformSequence()

	case EventFileUpsert:
		event.fileName = request.GetFileName()
		if err := checkLength("file_name", event.fileName, maxFileNameLen); err != nil {
			return nil, err
		}
		event.mimeType = strings.TrimSpace(request.GetMimeType())
		if err := checkLength("mime_type", event.mimeType, maxMimeTypeLen); err != nil {
			return nil, err
		}
		if request.GetSizeBytes() < 0 {
			return nil, invalidArgument("size_bytes must not be negative, got %d", request.GetSizeBytes())
		}
		event.sizeBytes = request.GetSizeBytes()
		event.contentSHA256 = strings.TrimSpace(request.GetContentSha256())
		if err := checkLength("content_sha256", event.contentSHA256, maxSHA256Len); err != nil {
			return nil, err
		}
		event.storageRef = strings.TrimSpace(request.GetStorageRef())
		if err := checkLength("storage_ref", event.storageRef, maxStorageRefLen); err != nil {
			return nil, err
		}
		uploadedAt, err := parseTimestamp("uploaded_at", request.GetUploadedAt())
		if err != nil {
			return nil, err
		}
		event.uploadedAt = uploadedAt
		// The file's uploader comes from its own field. sender_external_user_id is
		// the message sender and is never read here: an event that fills a message
		// field for a file record must not be silently accepted as if it were the
		// uploader. An absent uploader stays absent, and the service does not fill
		// it from external_user_id either.
		event.uploaderUserID = strings.TrimSpace(request.GetUploaderExternalUserId())
		if err := checkLength("uploader_external_user_id", event.uploaderUserID, maxExternalIDLen); err != nil {
			return nil, err
		}

	case EventMessageRecalled, EventFileRecalled:
		recalledAt, err := parseTimestamp("recalled_at", request.GetRecalledAt())
		if err != nil {
			return nil, err
		}
		event.recalledAt = recalledAt
		if err := checkLength("operator_external_user_id", event.operatorExternalID, maxExternalIDLen); err != nil {
			return nil, err
		}
	}

	return event, nil
}

// validateConversation accepts the conversation identity py-agent already
// derived instead of deriving a second one. conversation_id is used verbatim;
// the redundant fields are cross-checked against it. Recomputing the id here
// could disagree with py-agent and would then index a record under an identity
// its own recall event no longer matches.
func validateConversation(event *sourceEvent) error {
	parts := strings.Split(event.conversationID, ":")
	if len(parts) != 4 || parts[0] != channelQQ || parts[2] != event.conversationKind || strings.TrimSpace(parts[3]) == "" {
		return invalidArgument("conversation_id %q does not follow the py-agent convention qq:<bot_id>:<private|group>:<local_id>",
			event.conversationID)
	}
	if parts[1] != event.botID {
		return invalidArgument("conversation_id %q names bot %q but bot_id is %q", event.conversationID, parts[1], event.botID)
	}
	switch event.conversationKind {
	case conversationGroup:
		if event.externalGroupID == "" {
			return invalidArgument("external_group_id is required for a group conversation (%q)", event.conversationID)
		}
		if event.externalGroupID != parts[3] {
			return invalidArgument("conversation_id %q names group %q but external_group_id is %q",
				event.conversationID, parts[3], event.externalGroupID)
		}
		if err := checkLength("external_group_id", event.externalGroupID, maxExternalIDLen); err != nil {
			return err
		}
	case conversationPrivate:
		if event.externalGroupID != "" {
			return invalidArgument("external_group_id must be empty for a private conversation (%q), got %q",
				event.conversationID, event.externalGroupID)
		}
		if event.externalUserID != "" && event.externalUserID != parts[3] {
			return invalidArgument("conversation_id %q names user %q but external_user_id is %q",
				event.conversationID, parts[3], event.externalUserID)
		}
	}
	return checkLength("external_user_id", event.externalUserID, maxExternalIDLen)
}

// parseTimestamp reads one of the contract's RFC3339 string instants. An empty
// string means "not supplied"; an unparsable one is a producer bug.
func parseTimestamp(field, value string) (pgtype.Timestamptz, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return pgtype.Timestamptz{}, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, trimmed)
	if err != nil {
		return pgtype.Timestamptz{}, invalidArgument("%s %q is not an RFC3339 instant", field, value)
	}
	return pgtype.Timestamptz{Time: parsed.UTC(), Valid: true}, nil
}

// checkLength rejects a value the storage column could not hold, using
// characters (what varchar counts) rather than bytes.
func checkLength(field, value string, limit int) error {
	if utf8.RuneCountInString(value) > limit {
		return invalidArgument("%s is %d characters, the storage column accepts %d", field, utf8.RuneCountInString(value), limit)
	}
	return nil
}

// isUUID reports whether the value is a canonical 8-4-4-4-12 UUID literal.
func isUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for index, char := range value {
		switch index {
		case 8, 13, 18, 23:
			if char != '-' {
				return false
			}
		default:
			if !isHexDigit(char) {
				return false
			}
		}
	}
	return true
}

func isHexDigit(char rune) bool {
	switch {
	case char >= '0' && char <= '9':
		return true
	case char >= 'a' && char <= 'f':
		return true
	case char >= 'A' && char <= 'F':
		return true
	default:
		return false
	}
}

// equalTimestamptz compares two optional instants for payload identity.
func equalTimestamptz(left, right pgtype.Timestamptz) bool {
	if left.Valid != right.Valid {
		return false
	}
	if !left.Valid {
		return true
	}
	return left.Time.Equal(right.Time)
}

// isUniqueViolation reports whether the driver rejected a duplicate key. The
// ledger's two unique constraints are the only place this service can hit one.
func isUniqueViolation(err error) bool {
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) {
		return pgError.Code == "23505"
	}
	return false
}

// fenceDecision is the outcome of comparing an incoming event with the state
// already applied for one record.
type fenceDecision int

const (
	// fenceApply means the event is newer than everything applied.
	fenceApply fenceDecision = iota
	// fenceStaleRevision means a lower revision arrived after a higher one.
	fenceStaleRevision
	// fenceDuplicateRevision means the same revision with the same payload
	// arrived again: an idempotent replay, not an error.
	fenceDuplicateRevision
	// fenceConflict means the same revision arrived with a different payload,
	// or the record id is already bound to another conversation.
	fenceConflict
)

// String keeps test failures readable.
func (decision fenceDecision) String() string {
	switch decision {
	case fenceApply:
		return "apply"
	case fenceStaleRevision:
		return "stale_revision"
	case fenceDuplicateRevision:
		return "duplicate_revision"
	case fenceConflict:
		return "conflict"
	default:
		return "unknown"
	}
}

// identityMatchesMessage reports whether a stored message row belongs to the
// same conversation as the event.
//
// A record id is a primary key inside one corpus, so a second conversation
// claiming the same id would overwrite the first conversation's row. That is
// refused instead: raw records never migrate between conversations.
func identityMatchesMessage(event *sourceEvent, row *messageRow) bool {
	return row.botID == event.botID &&
		row.conversationID == event.conversationID &&
		row.conversationKind == event.conversationKind
}

// identityMatchesFile is the file model's own version of the same rule. It is
// deliberately not shared with the message one: the two rows have different
// columns and a single helper would have to be generic over both, which is the
// coupling this split exists to remove.
func identityMatchesFile(event *sourceEvent, row *fileRow) bool {
	return row.botID == event.botID &&
		row.conversationID == event.conversationID &&
		row.conversationKind == event.conversationKind
}

// decideMessageFence is the message lifecycle's order tolerance.
//
// appliedRevision is the newest revision this record has ever reached, taken as
// the newer of the stored row and the applied-event ledger.
func decideMessageFence(event *sourceEvent, appliedRevision int64, row *messageRow) fenceDecision {
	if row != nil && !identityMatchesMessage(event, row) {
		return fenceConflict
	}
	if appliedRevision > event.revision {
		return fenceStaleRevision
	}
	if appliedRevision == event.revision {
		if row != nil && event.eventKind == EventMessageUpsert && matchesMessageUpsert(event, row) {
			return fenceDuplicateRevision
		}
		if row != nil && event.eventKind == EventMessageRecalled && row.status == StatusRecalled {
			return fenceDuplicateRevision
		}
		return fenceConflict
	}
	return fenceApply
}

// decideFileFence is the file lifecycle's order tolerance. Same rule, own row
// type: files never share a lifecycle helper with messages.
func decideFileFence(event *sourceEvent, appliedRevision int64, row *fileRow) fenceDecision {
	if row != nil && !identityMatchesFile(event, row) {
		return fenceConflict
	}
	if appliedRevision > event.revision {
		return fenceStaleRevision
	}
	if appliedRevision == event.revision {
		if row != nil && event.eventKind == EventFileUpsert && matchesFileUpsert(event, row) {
			return fenceDuplicateRevision
		}
		if row != nil && event.eventKind == EventFileRecalled && row.status == StatusRecalled {
			return fenceDuplicateRevision
		}
		return fenceConflict
	}
	return fenceApply
}

// matchesMessageUpsert reports whether re-applying this upsert would change
// nothing at all.
func matchesMessageUpsert(event *sourceEvent, row *messageRow) bool {
	if row.status != StatusIndexed {
		return false
	}
	return row.externalUserID == event.externalUserID &&
		row.externalGroupID == event.externalGroupID &&
		row.senderExternalID == event.senderExternalID &&
		row.text == event.messageText &&
		row.platformSequence == event.platformSequence &&
		equalTimestamptz(row.sentAt, event.sentAt)
}

// matchesFileUpsert reports whether re-applying this upsert would change nothing
// at all.
func matchesFileUpsert(event *sourceEvent, row *fileRow) bool {
	if row.status != StatusIndexed {
		return false
	}
	return row.externalUserID == event.externalUserID &&
		row.externalGroupID == event.externalGroupID &&
		row.uploaderUserID == event.uploaderUserID &&
		row.fileName == event.fileName &&
		row.mimeType == event.mimeType &&
		row.sizeBytes == event.sizeBytes &&
		row.contentSHA256 == event.contentSHA256 &&
		row.storageRef == event.storageRef &&
		equalTimestamptz(row.uploadedAt, event.uploadedAt)
}

// conflictReason renders the reason string for a conflict decision so the
// message and file paths report the same vocabulary.
func conflictReason(event *sourceEvent, rowIdentityDiffers bool) string {
	if rowIdentityDiffers {
		return "record_id_is_bound_to_another_conversation"
	}
	if event.recalls() {
		return "record_is_already_indexed_at_this_revision"
	}
	return "record_is_already_applied_with_a_different_payload_at_this_revision"
}
