// Package pyagent is a TEST/INGRESS HELPER. It is not a production py-agent
// client and it contains no client, no stream and no endpoint: qq-search never
// dials py-agent on any path, and the only thing this package does is make the
// py-agent -> qq-search seam executable.
//
// It encodes exactly three things:
//
//  1. py-agent's own conversation identity. agent_bot.domain.models.InboundEvent
//     derives conversation_id as "qq:<bot_id>:group:<group_id>" or
//     "qq:<bot_id>:private:<user_id>". qq-search accepts that string verbatim and
//     never re-derives it, so the formula is mirrored here rather than invented.
//
//  2. The field mapping from a qqsource.v1.QQSourceEventEnvelope onto the flat
//     qqsearch.v1.IndexQQSourceEventRequest that IndexQQSourceEvent accepts.
//
//  3. The event-id seam. py-agent's event_id is an opaque producer id: for an
//     InboundEvent it is the hex sha256 of
//     "<conversation_id>:message:<message_id>". It goes on the wire unchanged;
//     qq-search derives its own ledger key from it (see ledgerEventID in the
//     application layer), so neither side has to conform to the other's storage.
package pyagent

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	qqsearchv1 "packages/gen/qqsearch/v1"
	qqsourcev1 "packages/gen/qqsource/v1"
)

// ChannelQQ is the only channel this service indexes.
const ChannelQQ = "qq"

// InboundEvent mirrors agent_bot.domain.models.InboundEvent field for field.
type InboundEvent struct {
	BotID     string `json:"bot_id"`
	UserID    string `json:"user_id"`
	MessageID string `json:"message_id"`
	Text      string `json:"text"`
	GroupID   string `json:"group_id,omitempty"`
	Mentioned bool   `json:"mentioned,omitempty"`
}

// ConversationID mirrors InboundEvent.conversation_id exactly, including the
// branch order: a group conversation is identified by the group, a private one by
// the user.
func (event InboundEvent) ConversationID() string {
	if event.GroupID != "" {
		return "qq:" + event.BotID + ":group:" + event.GroupID
	}
	return "qq:" + event.BotID + ":private:" + event.UserID
}

// ConversationKind is the "private" or "group" segment of the identity above.
func (event InboundEvent) ConversationKind() string {
	if event.GroupID != "" {
		return "group"
	}
	return "private"
}

// DerivedEventID mirrors InboundEvent.event_id: sha256 hex of
// "<conversation_id>:message:<message_id>". It is py-agent's ingress
// de-duplication key, not a UUID.
func (event InboundEvent) DerivedEventID() string {
	sum := sha256.Sum256([]byte(event.ConversationID() + ":message:" + event.MessageID))
	return hex.EncodeToString(sum[:])
}

// sourceEventID renders a producer-assigned event id the same way py-agent does
// for an inbound message: a hex sha256 over a stable name. It is opaque to
// qq-search, which derives its own ledger key from it.
func sourceEventID(name string) string {
	sum := sha256.Sum256([]byte(name))
	return hex.EncodeToString(sum[:])
}

// Conversation is a py-agent conversation context outside the InboundEvent path
// (files and recalls arrive with a conversation but no inbound text).
type Conversation struct {
	BotID           string
	ExternalUserID  string
	ExternalGroupID string
}

// ID renders the py-agent identity for this conversation.
func (conversation Conversation) ID() string {
	if conversation.ExternalGroupID != "" {
		return "qq:" + conversation.BotID + ":group:" + conversation.ExternalGroupID
	}
	return "qq:" + conversation.BotID + ":private:" + conversation.ExternalUserID
}

// Kind is the conversation kind segment.
func (conversation Conversation) Kind() string {
	if conversation.ExternalGroupID != "" {
		return "group"
	}
	return "private"
}

// kindEnum maps the kind string onto the qqsource.v1 enum.
func (conversation Conversation) kindEnum() qqsourcev1.QQConversationKind {
	if conversation.ExternalGroupID != "" {
		return qqsourcev1.QQConversationKind_QQ_CONVERSATION_KIND_GROUP
	}
	return qqsourcev1.QQConversationKind_QQ_CONVERSATION_KIND_PRIVATE
}

// protoConversation renders the conversation half of an envelope.
func (conversation Conversation) protoConversation() *qqsourcev1.QQConversation {
	return &qqsourcev1.QQConversation{
		Channel:         ChannelQQ,
		BotId:           conversation.BotID,
		Kind:            conversation.kindEnum(),
		ExternalUserId:  conversation.ExternalUserID,
		ExternalGroupId: conversation.ExternalGroupID,
		ConversationId:  conversation.ID(),
	}
}

// QQFile is the py-agent side of one original file.
type QQFile struct {
	RecordID       string
	UploaderUserID string
	FileName       string
	MimeType       string
	SizeBytes      int64
	ContentSHA256  string
	StorageRef     string
}

// MessageEnvelope builds the qqsource.v1 event for one InboundEvent.
func (event InboundEvent) MessageEnvelope(revision uint64, sequence int64, occurredAt time.Time) *qqsourcev1.QQSourceEventEnvelope {
	instant := occurredAt.UTC().Format(time.RFC3339Nano)
	conversation := Conversation{
		BotID:           event.BotID,
		ExternalUserID:  event.UserID,
		ExternalGroupID: event.GroupID,
	}
	return &qqsourcev1.QQSourceEventEnvelope{
		Sequence:       sequence,
		EventId:        event.DerivedEventID(),
		RecordRevision: revision,
		Kind:           qqsourcev1.QQSourceEventKind_QQ_SOURCE_EVENT_KIND_MESSAGE_UPSERT,
		Conversation:   conversation.protoConversation(),
		Message: &qqsourcev1.QQMessageRecord{
			RecordId:             event.MessageID,
			ConversationId:       event.ConversationID(),
			SenderExternalUserId: event.UserID,
			Text:                 event.Text,
			SentAt:               instant,
		},
		OccurredAt: instant,
	}
}

// FileEnvelope builds the qqsource.v1 event for one original file.
func FileEnvelope(conversation Conversation, file QQFile, revision uint64, sequence int64, occurredAt time.Time) *qqsourcev1.QQSourceEventEnvelope {
	instant := occurredAt.UTC().Format(time.RFC3339Nano)
	return &qqsourcev1.QQSourceEventEnvelope{
		Sequence:       sequence,
		EventId:        sourceEventID("file:" + conversation.ID() + ":" + file.RecordID + ":" + fmt.Sprint(revision)),
		RecordRevision: revision,
		Kind:           qqsourcev1.QQSourceEventKind_QQ_SOURCE_EVENT_KIND_FILE_UPSERT,
		Conversation:   conversation.protoConversation(),
		File: &qqsourcev1.QQFileRecord{
			RecordId:               file.RecordID,
			ConversationId:         conversation.ID(),
			UploaderExternalUserId: file.UploaderUserID,
			FileName:               file.FileName,
			MimeType:               file.MimeType,
			SizeBytes:              file.SizeBytes,
			ContentSha256:          file.ContentSHA256,
			StorageRef:             file.StorageRef,
			UploadedAt:             instant,
		},
		OccurredAt: instant,
	}
}

// RecallEnvelope builds a message_recalled event. RecordKind cannot be inferred
// from the record id, so the caller states which corpus it withdraws from.
func RecallEnvelope(conversation Conversation, recordID string, message bool, operatorUserID string, recalledAt time.Time, revision uint64, sequence int64) *qqsourcev1.QQSourceEventEnvelope {
	instant := recalledAt.UTC().Format(time.RFC3339Nano)
	kind := qqsourcev1.QQSourceEventKind_QQ_SOURCE_EVENT_KIND_FILE_RECALLED
	label := "file"
	if message {
		kind = qqsourcev1.QQSourceEventKind_QQ_SOURCE_EVENT_KIND_MESSAGE_RECALLED
		label = "message"
	}
	return &qqsourcev1.QQSourceEventEnvelope{
		Sequence:       sequence,
		EventId:        sourceEventID("recall:" + label + ":" + conversation.ID() + ":" + recordID + ":" + fmt.Sprint(revision)),
		RecordRevision: revision,
		Kind:           kind,
		Conversation:   conversation.protoConversation(),
		Recall: &qqsourcev1.QQRecallFact{
			RecordId:               recordID,
			ConversationId:         conversation.ID(),
			RecalledAt:             instant,
			OperatorExternalUserId: operatorUserID,
		},
		OccurredAt: instant,
	}
}

// RequestFromEnvelope flattens a qqsource.v1 envelope into the request
// IndexQQSourceEvent accepts. This is the mapping py-agent must implement when it
// pushes an event, written down once so it can be tested.
//
// The message sender and the file uploader are two separate fields
// (sender_external_user_id and uploader_external_user_id) and this mapping never
// fills one from the other: a producer that fills a message field for a file
// record must be visible rather than silently accepted.
func RequestFromEnvelope(envelope *qqsourcev1.QQSourceEventEnvelope) (*qqsearchv1.IndexQQSourceEventRequest, error) {
	if envelope == nil {
		return nil, fmt.Errorf("py-agent ingress: envelope is required")
	}
	conversation := envelope.GetConversation()
	if conversation == nil {
		return nil, fmt.Errorf("py-agent ingress: envelope.conversation is required")
	}
	request := &qqsearchv1.IndexQQSourceEventRequest{
		EventId:          envelope.GetEventId(),
		Sequence:         envelope.GetSequence(),
		RecordRevision:   envelope.GetRecordRevision(),
		Channel:          conversation.GetChannel(),
		BotId:            conversation.GetBotId(),
		ConversationKind: conversationKindName(conversation.GetKind()),
		ExternalUserId:   conversation.GetExternalUserId(),
		ExternalGroupId:  conversation.GetExternalGroupId(),
		ConversationId:   conversation.GetConversationId(),
		OccurredAt:       envelope.GetOccurredAt(),
	}
	switch envelope.GetKind() {
	case qqsourcev1.QQSourceEventKind_QQ_SOURCE_EVENT_KIND_MESSAGE_UPSERT:
		message := envelope.GetMessage()
		if message == nil {
			return nil, fmt.Errorf("py-agent ingress: message is required for message_upsert")
		}
		if err := checkSameConversation(conversation.GetConversationId(), message.GetConversationId()); err != nil {
			return nil, err
		}
		request.Kind = "message_upsert"
		request.RecordId = message.GetRecordId()
		request.MessageText = message.GetText()
		request.SenderExternalUserId = message.GetSenderExternalUserId()
		request.SentAt = message.GetSentAt()
		request.PlatformSequence = message.GetPlatformSequence()

	case qqsourcev1.QQSourceEventKind_QQ_SOURCE_EVENT_KIND_FILE_UPSERT:
		file := envelope.GetFile()
		if file == nil {
			return nil, fmt.Errorf("py-agent ingress: file is required for file_upsert")
		}
		if err := checkSameConversation(conversation.GetConversationId(), file.GetConversationId()); err != nil {
			return nil, err
		}
		request.Kind = "file_upsert"
		request.RecordId = file.GetRecordId()
		request.FileName = file.GetFileName()
		request.MimeType = file.GetMimeType()
		request.SizeBytes = file.GetSizeBytes()
		request.ContentSha256 = file.GetContentSha256()
		request.StorageRef = file.GetStorageRef()
		request.UploadedAt = file.GetUploadedAt()
		request.UploaderExternalUserId = file.GetUploaderExternalUserId()

	case qqsourcev1.QQSourceEventKind_QQ_SOURCE_EVENT_KIND_MESSAGE_RECALLED,
		qqsourcev1.QQSourceEventKind_QQ_SOURCE_EVENT_KIND_FILE_RECALLED:
		recall := envelope.GetRecall()
		if recall == nil {
			return nil, fmt.Errorf("py-agent ingress: recall is required for a recalled event")
		}
		if err := checkSameConversation(conversation.GetConversationId(), recall.GetConversationId()); err != nil {
			return nil, err
		}
		if envelope.GetKind() == qqsourcev1.QQSourceEventKind_QQ_SOURCE_EVENT_KIND_MESSAGE_RECALLED {
			request.Kind = "message_recalled"
		} else {
			request.Kind = "file_recalled"
		}
		request.RecordId = recall.GetRecordId()
		request.RecalledAt = recall.GetRecalledAt()
		request.OperatorExternalUserId = recall.GetOperatorExternalUserId()

	default:
		return nil, fmt.Errorf("py-agent ingress: unsupported event kind %s", envelope.GetKind())
	}
	return request, nil
}

// checkSameConversation refuses an envelope whose payload names a different
// conversation than its own conversation: the flat request carries one
// conversation_id, so the two must agree.
func checkSameConversation(envelopeConversationID, payloadConversationID string) error {
	if strings.TrimSpace(payloadConversationID) == "" {
		return fmt.Errorf("py-agent ingress: payload conversation_id is required")
	}
	if envelopeConversationID != payloadConversationID {
		return fmt.Errorf("py-agent ingress: payload names conversation %q but the envelope names %q",
			payloadConversationID, envelopeConversationID)
	}
	return nil
}

// conversationKindName renders the enum as the string the flat request carries.
func conversationKindName(kind qqsourcev1.QQConversationKind) string {
	switch kind {
	case qqsourcev1.QQConversationKind_QQ_CONVERSATION_KIND_PRIVATE:
		return "private"
	case qqsourcev1.QQConversationKind_QQ_CONVERSATION_KIND_GROUP:
		return "group"
	default:
		return ""
	}
}

// Transcript is the recorded py-agent InboundEvent stream used by the tests.
type Transcript struct {
	Events []InboundEvent `json:"events"`
}

//go:embed testdata/inbound_events.json
var transcriptJSON []byte

// LoadTranscript reads the embedded transcript of InboundEvent payloads.
func LoadTranscript() (Transcript, error) {
	var transcript Transcript
	if err := json.Unmarshal(transcriptJSON, &transcript); err != nil {
		return Transcript{}, fmt.Errorf("py-agent ingress: parse transcript: %w", err)
	}
	if len(transcript.Events) == 0 {
		return Transcript{}, fmt.Errorf("py-agent ingress: transcript is empty")
	}
	return transcript, nil
}
