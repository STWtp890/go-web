package pyagent

// Unit tests for the ingress helper: the py-agent conversation identity, the
// embedded transcript and the field mapping onto the flat request. The mapping is
// also replayed against the real service in the application integration suite.

import (
	"encoding/hex"
	"testing"
	"time"

	qqsourcev1 "packages/gen/qqsource/v1"

	"google.golang.org/protobuf/proto"
)

func TestTranscriptUsesThePyAgentConversationIdentity(t *testing.T) {
	transcript, err := LoadTranscript()
	if err != nil {
		t.Fatalf("LoadTranscript: %v", err)
	}
	if len(transcript.Events) != 3 {
		t.Fatalf("transcript has %d events, want 3", len(transcript.Events))
	}
	want := []string{"qq:10001:private:20002", "qq:10001:group:999", "qq:10001:group:999"}
	wantKind := []string{"private", "group", "group"}
	for index, event := range transcript.Events {
		if got := event.ConversationID(); got != want[index] {
			t.Errorf("event %d conversation id = %q, want %q", index, got, want[index])
		}
		if got := event.ConversationKind(); got != wantKind[index] {
			t.Errorf("event %d conversation kind = %q, want %q", index, got, wantKind[index])
		}
	}
	if transcript.Events[0].GroupID != "" {
		t.Errorf("the private transcript event carries group id %q", transcript.Events[0].GroupID)
	}
	if transcript.Events[1].GroupID != "999" {
		t.Errorf("the group transcript event carries group id %q, want 999", transcript.Events[1].GroupID)
	}
}

func TestDerivedEventIDMirrorsPyAgentsOwnFormula(t *testing.T) {
	event := InboundEvent{BotID: "10001", UserID: "20003", MessageID: "msg-1002", GroupID: "999"}
	id := event.DerivedEventID()
	if len(id) != 64 {
		t.Fatalf("derived event id %q is %d characters, want a 64-character hex sha256", id, len(id))
	}
	if _, err := hex.DecodeString(id); err != nil {
		t.Fatalf("derived event id %q is not hex: %v", id, err)
	}
	// py-agent hashes "<conversation_id>:message:<message_id>"; the same input
	// must give the same id so a redelivery stays a duplicate.
	same := InboundEvent{BotID: "10001", UserID: "20003", MessageID: "msg-1002", GroupID: "999"}
	if same.DerivedEventID() != id {
		t.Fatal("the event id is not deterministic")
	}
	other := InboundEvent{BotID: "10001", UserID: "20003", MessageID: "msg-1003", GroupID: "999"}
	if other.DerivedEventID() == id {
		t.Fatal("two different messages share an event id")
	}
}

func TestRequestFromEnvelopeMapsEveryMessageField(t *testing.T) {
	occurred := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	inbound := InboundEvent{BotID: "10001", UserID: "20003", MessageID: "msg-1002", Text: "hello quarterly", GroupID: "999", Mentioned: true}

	request, err := RequestFromEnvelope(inbound.MessageEnvelope(4, 11, occurred))
	if err != nil {
		t.Fatalf("RequestFromEnvelope: %v", err)
	}
	// Every field of the flat request is compared with the value py-agent sent.
	fields := []struct {
		name string
		want string
		got  string
	}{
		{"event_id", inbound.DerivedEventID(), request.GetEventId()},
		{"kind", "message_upsert", request.GetKind()},
		{"channel", "qq", request.GetChannel()},
		{"bot_id", "10001", request.GetBotId()},
		{"conversation_kind", "group", request.GetConversationKind()},
		{"external_group_id", "999", request.GetExternalGroupId()},
		{"external_user_id", "20003", request.GetExternalUserId()},
		{"conversation_id", "qq:10001:group:999", request.GetConversationId()},
		{"record_id", "msg-1002", request.GetRecordId()},
		{"message_text", "hello quarterly", request.GetMessageText()},
		{"sender_external_user_id", "20003", request.GetSenderExternalUserId()},
		{"sent_at", occurred.Format(time.RFC3339Nano), request.GetSentAt()},
		{"occurred_at", occurred.Format(time.RFC3339Nano), request.GetOccurredAt()},
		{"uploader_external_user_id", "", request.GetUploaderExternalUserId()},
	}
	for _, field := range fields {
		if field.got != field.want {
			t.Errorf("%s = %q, want %q", field.name, field.got, field.want)
		}
	}
	if request.GetRecordRevision() != 4 || request.GetSequence() != 11 {
		t.Errorf("revision/sequence = %d/%d, want 4/11", request.GetRecordRevision(), request.GetSequence())
	}
	if request.GetPlatformSequence() != 0 {
		t.Errorf("platform_sequence = %d, want 0 when py-agent does not supply one", request.GetPlatformSequence())
	}
}

func TestRequestFromEnvelopeMapsTheFileUploaderToItsOwnField(t *testing.T) {
	occurred := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	conversation := Conversation{BotID: "10001", ExternalGroupID: "999"}
	file := QQFile{
		RecordID:       "file-7",
		UploaderUserID: "20004",
		FileName:       "report.pdf",
		MimeType:       "application/pdf",
		SizeBytes:      2048,
		ContentSHA256:  "abc123",
		StorageRef:     "qq/10001/group/999/file-7",
	}
	request, err := RequestFromEnvelope(FileEnvelope(conversation, file, 2, 12, occurred))
	if err != nil {
		t.Fatalf("RequestFromEnvelope(file): %v", err)
	}
	if request.GetKind() != "file_upsert" {
		t.Fatalf("kind = %q, want file_upsert", request.GetKind())
	}
	if request.GetUploaderExternalUserId() != "20004" {
		t.Fatalf("uploader = %q, want 20004", request.GetUploaderExternalUserId())
	}
	if request.GetSenderExternalUserId() != "" {
		t.Fatalf("the file uploader leaked into sender_external_user_id: %q", request.GetSenderExternalUserId())
	}
	if request.GetFileName() != "report.pdf" || request.GetMimeType() != "application/pdf" ||
		request.GetSizeBytes() != 2048 || request.GetStorageRef() != "qq/10001/group/999/file-7" {
		t.Fatalf("file fields were not mapped: %+v", request)
	}
	if request.GetConversationId() != "qq:10001:group:999" {
		t.Fatalf("conversation = %q, want qq:10001:group:999", request.GetConversationId())
	}

	recall, err := RequestFromEnvelope(RecallEnvelope(conversation, "file-7", false, "20005", occurred, 3, 13))
	if err != nil {
		t.Fatalf("RequestFromEnvelope(file recall): %v", err)
	}
	if recall.GetKind() != "file_recalled" || recall.GetRecordId() != "file-7" {
		t.Fatalf("file recall mapped to %q/%q", recall.GetKind(), recall.GetRecordId())
	}
	if recall.GetOperatorExternalUserId() != "20005" {
		t.Fatalf("operator = %q, want 20005", recall.GetOperatorExternalUserId())
	}
	if recall.GetUploaderExternalUserId() != "" || recall.GetSenderExternalUserId() != "" {
		t.Fatal("a recall must not carry a sender or an uploader")
	}

	message, err := RequestFromEnvelope(RecallEnvelope(conversation, "msg-1002", true, "20005", occurred, 3, 14))
	if err != nil {
		t.Fatalf("RequestFromEnvelope(message recall): %v", err)
	}
	if message.GetKind() != "message_recalled" {
		t.Fatalf("message recall mapped to %q", message.GetKind())
	}
}

func TestRequestFromEnvelopeRefusesMismatchedOrUnknownPayloads(t *testing.T) {
	inbound := InboundEvent{BotID: "10001", UserID: "20003", MessageID: "msg-1002", Text: "hi", GroupID: "999"}
	envelope := inbound.MessageEnvelope(1, 1, time.Now().UTC())

	t.Run("payload names another conversation", func(t *testing.T) {
		broken := proto.Clone(envelope).(*qqsourcev1.QQSourceEventEnvelope)
		broken.Message.ConversationId = "qq:10001:group:1000"
		if _, err := RequestFromEnvelope(broken); err == nil {
			t.Fatal("a payload naming another conversation must be refused: the flat request carries one id")
		}
	})

	t.Run("unknown event kind", func(t *testing.T) {
		broken := proto.Clone(envelope).(*qqsourcev1.QQSourceEventEnvelope)
		broken.Kind = qqsourcev1.QQSourceEventKind_QQ_SOURCE_EVENT_KIND_UNSPECIFIED
		if _, err := RequestFromEnvelope(broken); err == nil {
			t.Fatal("an unspecified event kind must be refused")
		}
	})

	t.Run("missing payload for the kind", func(t *testing.T) {
		broken := proto.Clone(envelope).(*qqsourcev1.QQSourceEventEnvelope)
		broken.Message = nil
		if _, err := RequestFromEnvelope(broken); err == nil {
			t.Fatal("a message_upsert without a message payload must be refused")
		}
	})

	t.Run("no envelope", func(t *testing.T) {
		if _, err := RequestFromEnvelope(nil); err == nil {
			t.Fatal("a nil envelope must be refused")
		}
	})

	t.Run("no conversation", func(t *testing.T) {
		broken := proto.Clone(envelope).(*qqsourcev1.QQSourceEventEnvelope)
		broken.Conversation = nil
		if _, err := RequestFromEnvelope(broken); err == nil {
			t.Fatal("an envelope without a conversation must be refused")
		}
	})
}
