package application

// Shared fixtures for the unit tests in this package. They build the same shapes
// py-agent sends, so a change to the event contract breaks these tests rather
// than silently changing what the service accepts.

import (
	"testing"

	qqsearchv1 "packages/gen/qqsearch/v1"
)

// testGroupConversation is the group identity from the py-agent convention:
// qq:<bot_id>:group:<group_id>.
const (
	testBotID          = "10001"
	testGroupID        = "999"
	testPrivateUserID  = "20002"
	testGroupEventUUID = "3f1a9c62-0d1b-4b3f-9a2e-1c2d3e4f5a6b"
)

// validMessageRequest is a fully valid message_upsert for the group conversation.
func validMessageRequest() *qqsearchv1.IndexQQSourceEventRequest {
	return &qqsearchv1.IndexQQSourceEventRequest{
		EventId:              testGroupEventUUID,
		Sequence:             7,
		RecordRevision:       1,
		Kind:                 EventMessageUpsert,
		Channel:              "qq",
		BotId:                testBotID,
		ConversationKind:     conversationGroup,
		ExternalGroupId:      testGroupID,
		ConversationId:       "qq:" + testBotID + ":group:" + testGroupID,
		RecordId:             "msg-1",
		MessageText:          "hello quarterly report",
		SenderExternalUserId: "20003",
		SentAt:               "2026-09-26T10:00:00Z",
		OccurredAt:           "2026-09-26T10:00:01Z",
	}
}

// validFileRequest is a fully valid file_upsert for the group conversation.
func validFileRequest() *qqsearchv1.IndexQQSourceEventRequest {
	return &qqsearchv1.IndexQQSourceEventRequest{
		EventId:                "9c1b7e44-2a35-4c6d-8f10-55aa66bb77cc",
		Sequence:               8,
		RecordRevision:         1,
		Kind:                   EventFileUpsert,
		Channel:                "qq",
		BotId:                  testBotID,
		ConversationKind:       conversationGroup,
		ExternalGroupId:        testGroupID,
		ConversationId:         "qq:" + testBotID + ":group:" + testGroupID,
		RecordId:               "file-1",
		FileName:               "quarterly-report.pdf",
		MimeType:               "application/pdf",
		SizeBytes:              4096,
		ContentSha256:          "b1946ac92492d2347c6235b4d2611184",
		StorageRef:             "qq/10001/group/999/file-1",
		UploadedAt:             "2026-09-26T10:05:00Z",
		UploaderExternalUserId: "20003",
		OccurredAt:             "2026-09-26T10:05:01Z",
	}
}

// recallRequest is a fully valid recall event for the given record kind.
func recallRequest(kind RecordKind) *qqsearchv1.IndexQQSourceEventRequest {
	eventKind := EventMessageRecalled
	recordID := "msg-1"
	if kind == RecordKindFile {
		eventKind = EventFileRecalled
		recordID = "file-1"
	}
	return &qqsearchv1.IndexQQSourceEventRequest{
		EventId:                "c0ffee00-1111-4222-8333-444455556666",
		Sequence:               9,
		RecordRevision:         2,
		Kind:                   eventKind,
		Channel:                "qq",
		BotId:                  testBotID,
		ConversationKind:       conversationGroup,
		ExternalGroupId:        testGroupID,
		ConversationId:         "qq:" + testBotID + ":group:" + testGroupID,
		RecordId:               recordID,
		RecalledAt:             "2026-09-26T11:00:00Z",
		OperatorExternalUserId: "20003",
		OccurredAt:             "2026-09-26T11:00:01Z",
	}
}

// mustEvent validates a request the way the service does, failing the test when
// the fixture itself is wrong.
func mustEvent(t *testing.T, request *qqsearchv1.IndexQQSourceEventRequest) *sourceEvent {
	t.Helper()
	event, err := parseSourceEvent(request)
	if err != nil {
		t.Fatalf("parseSourceEvent(%v) returned %v, want a valid event", request, err)
	}
	return event
}

// messageRowFrom is the stored message row this event would have produced.
func messageRowFrom(event *sourceEvent, revision int64, state RecordStatus) *messageRow {
	return &messageRow{
		botID:            event.botID,
		conversationID:   event.conversationID,
		conversationKind: event.conversationKind,
		externalUserID:   event.externalUserID,
		externalGroupID:  event.externalGroupID,
		revision:         revision,
		status:           state,
		senderExternalID: event.senderExternalID,
		text:             event.messageText,
		sentAt:           event.sentAt,
		platformSequence: event.platformSequence,
	}
}

// fileRowFrom is the stored file row this event would have produced.
func fileRowFrom(event *sourceEvent, revision int64, state RecordStatus) *fileRow {
	return &fileRow{
		botID:            event.botID,
		conversationID:   event.conversationID,
		conversationKind: event.conversationKind,
		externalUserID:   event.externalUserID,
		externalGroupID:  event.externalGroupID,
		revision:         revision,
		status:           state,
		uploaderUserID:   event.uploaderUserID,
		fileName:         event.fileName,
		mimeType:         event.mimeType,
		sizeBytes:        event.sizeBytes,
		contentSHA256:    event.contentSHA256,
		storageRef:       event.storageRef,
		uploadedAt:       event.uploadedAt,
	}
}
