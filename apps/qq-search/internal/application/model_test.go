package application

// Unit tests for the parts of the service that need no database: event
// validation, the conversation identity, the revision fence and recall
// semantics, the two models' separation, and the channel-scope rules.

import (
	"context"
	"strings"
	"testing"

	"qq-search/internal/config"

	qqsearchv1 "packages/gen/qqsearch/v1"
	"packages/serviceauth"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestAdvertisedEventKindsExcludeDelete(t *testing.T) {
	// The contract advertises exactly four kinds. There is no delete kind: a
	// recalled record stays in its table, so a client has no path that removes
	// one.
	if len(AdvertisedEventKinds) != 4 {
		t.Fatalf("AdvertisedEventKinds has %d entries, want 4: %v", len(AdvertisedEventKinds), AdvertisedEventKinds)
	}
	for _, kind := range AdvertisedEventKinds {
		if strings.Contains(kind, "delete") || strings.Contains(kind, "remove") {
			t.Errorf("advertised kind %q is a delete kind; recall must stay append-only", kind)
		}
	}
	for _, kind := range []string{"message_deleted", "file_deleted", "message_delete", "deleted", ""} {
		request := validMessageRequest()
		request.Kind = kind
		if _, err := parseSourceEvent(request); status.Code(err) != codes.InvalidArgument {
			t.Errorf("kind %q: got %v, want INVALID_ARGUMENT", kind, err)
		}
	}
}

func TestParseSourceEventAcceptsEveryAdvertisedKind(t *testing.T) {
	for _, request := range []*qqsearchv1.IndexQQSourceEventRequest{
		validMessageRequest(),
		validFileRequest(),
		recallRequest(RecordKindMessage),
		recallRequest(RecordKindFile),
	} {
		event := mustEvent(t, request)
		if event.eventKind != request.GetKind() {
			t.Errorf("event kind %q, want %q", event.eventKind, request.GetKind())
		}
	}
}

func TestParseSourceEventRejectsMalformedIdentity(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*qqsearchv1.IndexQQSourceEventRequest)
	}{
		{"wrong channel", func(r *qqsearchv1.IndexQQSourceEventRequest) { r.Channel = "discord" }},
		{"empty bot id", func(r *qqsearchv1.IndexQQSourceEventRequest) { r.BotId = "" }},
		{"empty record id", func(r *qqsearchv1.IndexQQSourceEventRequest) { r.RecordId = "" }},
		{"unknown conversation kind", func(r *qqsearchv1.IndexQQSourceEventRequest) {
			r.ConversationKind = "channel"
			r.ConversationId = "qq:10001:channel:999"
		}},
		{"group identity for a private kind", func(r *qqsearchv1.IndexQQSourceEventRequest) {
			r.ConversationKind = conversationPrivate
			r.ExternalGroupId = ""
			r.ExternalUserId = testGroupID
		}},
		{"bot segment disagrees with bot id", func(r *qqsearchv1.IndexQQSourceEventRequest) {
			r.ConversationId = "qq:99999:group:999"
		}},
		{"group without external group id", func(r *qqsearchv1.IndexQQSourceEventRequest) { r.ExternalGroupId = "" }},
		{"external group id disagrees with the identity", func(r *qqsearchv1.IndexQQSourceEventRequest) { r.ExternalGroupId = "888" }},
		{"private conversation carrying a group id", func(r *qqsearchv1.IndexQQSourceEventRequest) {
			r.ConversationKind = conversationPrivate
			r.ConversationId = "qq:10001:private:20002"
			r.ExternalUserId = testPrivateUserID
		}},
		{"conversation id without the qq prefix", func(r *qqsearchv1.IndexQQSourceEventRequest) {
			r.ConversationId = "10001:group:999"
		}},
		{"conversation id with a trailing colon", func(r *qqsearchv1.IndexQQSourceEventRequest) {
			r.ConversationId = "qq:10001:group:"
		}},
		{"blank event id", func(r *qqsearchv1.IndexQQSourceEventRequest) { r.EventId = "   " }},
		{"over-long event id", func(r *qqsearchv1.IndexQQSourceEventRequest) { r.EventId = strings.Repeat("e", maxEventIDLen+1) }},
		{"zero revision", func(r *qqsearchv1.IndexQQSourceEventRequest) { r.RecordRevision = 0 }},
		{"negative sequence", func(r *qqsearchv1.IndexQQSourceEventRequest) { r.Sequence = -1 }},
		{"negative size", func(r *qqsearchv1.IndexQQSourceEventRequest) {
			r.Kind = EventFileUpsert
			r.SizeBytes = -1
		}},
		{"malformed sent_at", func(r *qqsearchv1.IndexQQSourceEventRequest) { r.SentAt = "yesterday" }},
		{"malformed occurred_at", func(r *qqsearchv1.IndexQQSourceEventRequest) { r.OccurredAt = "2026-13-45T99:00:00Z" }},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			request := validMessageRequest()
			testCase.mutate(request)
			if _, err := parseSourceEvent(request); status.Code(err) != codes.InvalidArgument {
				t.Fatalf("got %v, want INVALID_ARGUMENT", err)
			}
		})
	}
}

func TestParseSourceEventAcceptsThePyAgentConversationIdentity(t *testing.T) {
	// "qq:<bot_id>:group:<group_id>" is used verbatim, never re-derived.
	group := mustEvent(t, validMessageRequest())
	if group.conversationID != "qq:10001:group:999" {
		t.Fatalf("conversation id %q, want qq:10001:group:999", group.conversationID)
	}
	if group.recordKind != RecordKindMessage {
		t.Fatalf("record kind %q, want message", group.recordKind)
	}

	private := validMessageRequest()
	private.ConversationKind = conversationPrivate
	private.ConversationId = "qq:10001:private:20002"
	private.ExternalGroupId = ""
	private.ExternalUserId = testPrivateUserID
	event := mustEvent(t, private)
	if event.conversationID != "qq:10001:private:20002" {
		t.Fatalf("conversation id %q, want qq:10001:private:20002", event.conversationID)
	}
	if event.externalGroupID != "" {
		t.Fatalf("private conversation carried group id %q", event.externalGroupID)
	}
}

func TestLedgerEventIDIsADeterministicUUIDDerivedFromTheProducerID(t *testing.T) {
	// py-agent's own id is a hex sha256 and is not a UUID. The service keeps the
	// producer id opaque and derives the ledger key itself.
	producerID := strings.Repeat("a", 64)
	derived := ledgerEventID(producerID)
	if !isUUID(derived) {
		t.Fatalf("ledgerEventID(%q) = %q, want a canonical UUID", producerID, derived)
	}
	if derived == producerID {
		t.Fatal("the producer id must be derived, not stored verbatim")
	}
	if again := ledgerEventID(producerID); again != derived {
		t.Fatalf("the derivation is not deterministic: %q then %q", derived, again)
	}
	if other := ledgerEventID(producerID + "x"); other == derived {
		t.Fatal("two different producer ids collapsed onto one ledger key")
	}
	// A producer that already sends a UUID is derived as well: one rule, one
	// behaviour, no branch that quietly stores the raw value.
	uuidShaped := "3f1a9c62-0d1b-4b3f-9a2e-1c2d3e4f5a6b"
	if ledgerEventID(uuidShaped) == uuidShaped {
		t.Fatal("a UUID-shaped producer id must still be derived")
	}
	// The version and variant nibbles of the name-based layout are set.
	if derived[14] != '5' {
		t.Fatalf("derived UUID %q is not in the version-5 layout", derived)
	}
	switch derived[19] {
	case '8', '9', 'a', 'b':
	default:
		t.Fatalf("derived UUID %q does not carry the RFC 4122 variant", derived)
	}
}

func TestTheFileUploaderIsNeverAliasedFromTheMessageSender(t *testing.T) {
	// A file event that fills only the message sender field carries no uploader.
	fileRequest := validFileRequest()
	fileRequest.UploaderExternalUserId = ""
	fileRequest.SenderExternalUserId = "20099"
	event := mustEvent(t, fileRequest)
	if event.uploaderUserID != "" {
		t.Fatalf("the uploader was filled from sender_external_user_id: %q", event.uploaderUserID)
	}
	// And the dedicated field is what carries it.
	fileRequest.UploaderExternalUserId = "20088"
	event = mustEvent(t, fileRequest)
	if event.uploaderUserID != "20088" {
		t.Fatalf("uploader = %q, want 20088 from uploader_external_user_id", event.uploaderUserID)
	}

	// A message event ignores the uploader field: the message sender comes from
	// its own field only.
	messageRequest := validMessageRequest()
	messageRequest.UploaderExternalUserId = "20077"
	message := mustEvent(t, messageRequest)
	if message.senderExternalID != "20003" {
		t.Fatalf("message sender = %q, want 20003 from sender_external_user_id", message.senderExternalID)
	}
}

func TestMessageAndFileModelsAreSeparate(t *testing.T) {
	if messageTable == fileTable {
		t.Fatal("messages and files share a table")
	}
	messageStatements := []struct {
		name string
		sql  string
	}{
		{"messageUpsertStatement", messageUpsertStatement},
		{"messageRecallStatement", messageRecallStatement},
		{"messageRowStatement", messageRowStatement},
		{"messageStateStatement", messageStateStatement},
		{"messageSearchHead", messageSearchHead},
	}
	for _, statement := range messageStatements {
		if !strings.Contains(statement.sql, messageTable) {
			t.Errorf("%s does not read or write %s", statement.name, messageTable)
		}
		if strings.Contains(statement.sql, fileTable) {
			t.Errorf("%s references %s: a message query could return a file", statement.name, fileTable)
		}
		if !strings.Contains(statement.sql, "qq_search.") {
			t.Errorf("%s is not schema-qualified", statement.name)
		}
	}
	fileStatements := []struct {
		name string
		sql  string
	}{
		{"fileUpsertStatement", fileUpsertStatement},
		{"fileRecallStatement", fileRecallStatement},
		{"fileRowStatement", fileRowStatement},
		{"fileStateStatement", fileStateStatement},
		{"fileSearchHead", fileSearchHead},
	}
	for _, statement := range fileStatements {
		if !strings.Contains(statement.sql, fileTable) {
			t.Errorf("%s does not read or write %s", statement.name, fileTable)
		}
		if strings.Contains(statement.sql, messageTable) {
			t.Errorf("%s references %s: a file query could return a message", statement.name, messageTable)
		}
		if !strings.Contains(statement.sql, "qq_search.") {
			t.Errorf("%s is not schema-qualified", statement.name)
		}
	}

	// The payload columns stay separate too: the message model has no file
	// column and the file model has no message column.
	if !strings.Contains(messageSearchHead, "text_content") || strings.Contains(messageSearchHead, "file_name") {
		t.Error("the message query must project text_content and never file_name")
	}
	if !strings.Contains(fileSearchHead, "file_name") || strings.Contains(fileSearchHead, "text_content") {
		t.Error("the file query must project file_name and never text_content")
	}
	if !strings.Contains(messageRecallStatement, "text_content") || strings.Contains(messageRecallStatement, "file_name") {
		t.Error("the message recall statement must name message columns only")
	}
	if !strings.Contains(fileRecallStatement, "file_name") || strings.Contains(fileRecallStatement, "text_content") {
		t.Error("the file recall statement must name file columns only")
	}
}

func TestDecideMessageFenceRevisionRules(t *testing.T) {
	// The base fixture carries record_revision 1.
	upsert := mustEvent(t, validMessageRequest())
	newerRequest := validMessageRequest()
	newerRequest.RecordRevision = 3
	newer := mustEvent(t, newerRequest)

	cases := []struct {
		name     string
		event    *sourceEvent
		applied  int64
		row      *messageRow
		expected fenceDecision
	}{
		{
			name:     "a newer revision applies",
			event:    newer,
			applied:  2,
			row:      messageRowFrom(newer, 2, StatusIndexed),
			expected: fenceApply,
		},
		{
			name:     "first event for a record applies",
			event:    upsert,
			applied:  0,
			row:      nil,
			expected: fenceApply,
		},
		{
			name:     "a newer revision applies with no stored row",
			event:    newer,
			applied:  2,
			row:      nil,
			expected: fenceApply,
		},
		{
			name:     "lower revision is stale",
			event:    upsert,
			applied:  3,
			row:      messageRowFrom(upsert, 3, StatusIndexed),
			expected: fenceStaleRevision,
		},
		{
			name:     "same revision with the same payload is a duplicate",
			event:    upsert,
			applied:  1,
			row:      messageRowFrom(upsert, 1, StatusIndexed),
			expected: fenceDuplicateRevision,
		},
		{
			name:     "same revision with a different payload conflicts",
			event:    upsert,
			applied:  1,
			row:      func() *messageRow { row := messageRowFrom(upsert, 1, StatusIndexed); row.text = "edited"; return row }(),
			expected: fenceConflict,
		},
		{
			name:    "the same revision of another conversation conflicts",
			event:   upsert,
			applied: 1,
			row: func() *messageRow {
				row := messageRowFrom(upsert, 1, StatusIndexed)
				row.conversationID = "qq:10001:group:1000"
				return row
			}(),
			expected: fenceConflict,
		},
		{
			name:     "another bot claiming the same record id conflicts",
			event:    upsert,
			applied:  1,
			row:      func() *messageRow { row := messageRowFrom(upsert, 1, StatusIndexed); row.botID = "10002"; return row }(),
			expected: fenceConflict,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := decideMessageFence(testCase.event, testCase.applied, testCase.row); got != testCase.expected {
				t.Fatalf("decideMessageFence = %s, want %s", got, testCase.expected)
			}
		})
	}
}

func TestRecallSemanticsNeverResurrect(t *testing.T) {
	upsert := mustEvent(t, validMessageRequest())
	recall := mustEvent(t, recallRequest(RecordKindMessage))

	recalledRow := messageRowFrom(upsert, 2, StatusRecalled)

	// A late upsert at a lower revision than the recall is ignored, so a recall
	// cannot be undone by an out-of-order event.
	if got := decideMessageFence(upsert, 2, recalledRow); got != fenceStaleRevision {
		t.Fatalf("late upsert at a lower revision = %s, want stale_revision", got)
	}

	// A recall replayed at its own revision is a duplicate, not a second fact.
	if got := decideMessageFence(recall, 2, recalledRow); got != fenceDuplicateRevision {
		t.Fatalf("replayed recall = %s, want duplicate_revision", got)
	}

	// A recall that arrives at the revision already holding an indexed payload
	// is a conflict: one revision cannot mean both things.
	indexedRow := messageRowFrom(upsert, 2, StatusIndexed)
	if got := decideMessageFence(recall, 2, indexedRow); got != fenceConflict {
		t.Fatalf("recall at an indexed revision = %s, want conflict", got)
	}

	// A genuinely newer upsert is a newer source fact and does re-index.
	u := validMessageRequest()
	u.RecordRevision = 3
	u.MessageText = "edited after the recall"
	newer := mustEvent(t, u)
	if got := decideMessageFence(newer, 2, recalledRow); got != fenceApply {
		t.Fatalf("newer upsert after a recall = %s, want apply", got)
	}
}

func TestFileFenceUsesTheFileModelsOwnRules(t *testing.T) {
	upsert := mustEvent(t, validFileRequest())
	recall := mustEvent(t, recallRequest(RecordKindFile))

	if got := decideFileFence(upsert, 2, fileRowFrom(upsert, 2, StatusIndexed)); got != fenceStaleRevision {
		t.Fatalf("stale file revision = %s, want stale_revision", got)
	}
	if got := decideFileFence(upsert, 1, fileRowFrom(upsert, 1, StatusIndexed)); got != fenceDuplicateRevision {
		t.Fatalf("duplicate file revision = %s, want duplicate_revision", got)
	}
	edited := fileRowFrom(upsert, 1, StatusIndexed)
	edited.fileName = "other.pdf"
	if got := decideFileFence(upsert, 1, edited); got != fenceConflict {
		t.Fatalf("same revision different file payload = %s, want conflict", got)
	}
	if got := decideFileFence(recall, 2, fileRowFrom(upsert, 2, StatusRecalled)); got != fenceDuplicateRevision {
		t.Fatalf("replayed file recall = %s, want duplicate_revision", got)
	}
	if got := decideFileFence(recall, 2, fileRowFrom(upsert, 2, StatusIndexed)); got != fenceConflict {
		t.Fatalf("file recall at an indexed revision = %s, want conflict", got)
	}
}

func TestResolveScopeNarrowsAndRefusesToWiden(t *testing.T) {
	granted := &qqsearchv1.QQChannelScope{
		BotIds:           []string{testBotID},
		ConversationIds:  []string{"qq:10001:group:999", "qq:10001:group:1000"},
		ExternalGroupIds: []string{"999", "1000"},
	}

	t.Run("empty request keeps the whole grant", func(t *testing.T) {
		scope, err := resolveScope(granted, nil)
		if err != nil {
			t.Fatalf("resolveScope returned %v", err)
		}
		if len(scope.botIDs) != 1 || len(scope.conversationIDs) != 2 || len(scope.externalGroupIDs) != 2 {
			t.Fatalf("scope = %+v, want the granted scope unchanged", scope)
		}
	})

	t.Run("request narrows to one conversation", func(t *testing.T) {
		scope, err := resolveScope(granted, &qqsearchv1.QQChannelScope{
			ConversationIds: []string{"qq:10001:group:1000"},
		})
		if err != nil {
			t.Fatalf("resolveScope returned %v", err)
		}
		if len(scope.conversationIDs) != 1 || scope.conversationIDs[0] != "qq:10001:group:1000" {
			t.Fatalf("conversation scope = %v, want only qq:10001:group:1000", scope.conversationIDs)
		}
		if len(scope.botIDs) != 1 || len(scope.externalGroupIDs) != 2 {
			t.Fatalf("untouched families changed: %+v", scope)
		}
	})

	t.Run("out-of-scope conversation is refused as a whole", func(t *testing.T) {
		_, err := resolveScope(granted, &qqsearchv1.QQChannelScope{
			ConversationIds: []string{"qq:99999:group:1"},
		})
		if err == nil {
			t.Fatal("an out-of-scope conversation must be refused, not trimmed")
		}
		if got := status.Code(serviceauth.GRPCStatus(err)); got != codes.PermissionDenied {
			t.Fatalf("status = %s, want PermissionDenied", got)
		}
	})

	t.Run("out-of-scope group id is refused", func(t *testing.T) {
		if _, err := resolveScope(granted, &qqsearchv1.QQChannelScope{ExternalGroupIds: []string{"4242"}}); err == nil {
			t.Fatal("an out-of-scope group id must be refused")
		}
	})

	t.Run("an empty grant never widens", func(t *testing.T) {
		scope, err := resolveScope(&qqsearchv1.QQChannelScope{}, nil)
		if err != nil {
			t.Fatalf("resolveScope returned %v", err)
		}
		if !scope.empty() {
			t.Fatalf("scope %+v must stay empty", scope)
		}
	})

	t.Run("no capability at all is refused", func(t *testing.T) {
		if _, err := resolveScope(nil, nil); err == nil {
			t.Fatal("a query without a granted scope must be refused")
		}
	})
}

func TestEmptyGrantReturnsZeroHitsWithoutTouchingTheDatabase(t *testing.T) {
	// The service is assembled with no pool on purpose: an empty scope must
	// return before any query is attempted. If a future change let an empty
	// grant fall through to a query, this test would panic instead of quietly
	// returning every row.
	service := &Service{cfg: config.Default()}

	messages, err := service.searchMessages(context.Background(), &qqsearchv1.QQChannelScope{},
		&qqsearchv1.SearchQQMessagesRequest{Query: "quarterly"})
	if err != nil {
		t.Fatalf("empty-scope message search returned %v", err)
	}
	if len(messages.GetHits()) != 0 || messages.GetTruncated() {
		t.Fatalf("empty-scope message search returned %d hits (truncated=%v), want none",
			len(messages.GetHits()), messages.GetTruncated())
	}

	files, err := service.searchFiles(context.Background(), &qqsearchv1.QQChannelScope{},
		&qqsearchv1.SearchQQFilesRequest{Query: "quarterly"})
	if err != nil {
		t.Fatalf("empty-scope file search returned %v", err)
	}
	if len(files.GetHits()) != 0 || files.GetTruncated() {
		t.Fatalf("empty-scope file search returned %d hits (truncated=%v), want none",
			len(files.GetHits()), files.GetTruncated())
	}
}

func TestSearchRejectsABlankQuery(t *testing.T) {
	service := &Service{cfg: config.Default()}
	if _, err := service.searchMessages(context.Background(), &qqsearchv1.QQChannelScope{},
		&qqsearchv1.SearchQQMessagesRequest{Query: "   "}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("blank message query: got %v, want INVALID_ARGUMENT", err)
	}
	if _, err := service.searchFiles(context.Background(), &qqsearchv1.QQChannelScope{},
		&qqsearchv1.SearchQQFilesRequest{Query: ""}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("blank file query: got %v, want INVALID_ARGUMENT", err)
	}
}

func TestRecordKindSelectorRejectsUnspecified(t *testing.T) {
	service := &Service{cfg: config.Default()}
	granted := &qqsearchv1.QQChannelScope{}
	if _, err := service.GetRecordState(context.Background(), granted, &qqsearchv1.GetQQRecordStateRequest{
		Kind:     qqsearchv1.QQRecordKind_QQ_RECORD_KIND_UNSPECIFIED,
		RecordId: "msg-1",
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("unspecified kind: got %v, want INVALID_ARGUMENT", err)
	}
	if _, err := service.GetRecordState(context.Background(), granted, &qqsearchv1.GetQQRecordStateRequest{
		Kind: qqsearchv1.QQRecordKind_QQ_RECORD_KIND_MESSAGE,
	}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("empty record id: got %v, want INVALID_ARGUMENT", err)
	}
}

func TestValidateCollectionIdentityRefusesASharedNamespace(t *testing.T) {
	message := collectionIdentity{alias: "qq_source_messages_v1", domain: "qq-search:messages:v1"}
	file := collectionIdentity{alias: "qq_source_files_v1", domain: "qq-search:files:v1"}
	if err := validateCollectionIdentity(message, file); err != nil {
		t.Fatalf("distinct collections were rejected: %v", err)
	}
	if err := validateCollectionIdentity(message, message); err == nil {
		t.Fatal("a shared alias and domain must be refused")
	}
	if err := validateCollectionIdentity(message, collectionIdentity{alias: message.alias, domain: file.domain}); err == nil {
		t.Fatal("a shared alias must be refused even with distinct domains")
	}
	if err := validateCollectionIdentity(message, collectionIdentity{alias: file.alias, domain: message.domain}); err == nil {
		t.Fatal("a shared storage domain must be refused even with distinct aliases")
	}
	if err := validateCollectionIdentity(collectionIdentity{}, file); err == nil {
		t.Fatal("a missing message collection must be refused")
	}
}

func TestStatusMappingIsExplicit(t *testing.T) {
	if got := protoStatus(StatusRecalled); got != qqsearchv1.QQRecordStatus_QQ_RECORD_STATUS_RECALLED {
		t.Fatalf("recalled maps to %s, want QQ_RECORD_STATUS_RECALLED", got)
	}
	if got := protoStatus(StatusIndexed); got != qqsearchv1.QQRecordStatus_QQ_RECORD_STATUS_INDEXED {
		t.Fatalf("indexed maps to %s, want QQ_RECORD_STATUS_INDEXED", got)
	}
	if got := protoStatus("something-else"); got != qqsearchv1.QQRecordStatus_QQ_RECORD_STATUS_UNSPECIFIED {
		t.Fatalf("an unknown stored state maps to %s, want UNSPECIFIED", got)
	}
	if got := protoRecordKind(RecordKindFile); got != qqsearchv1.QQRecordKind_QQ_RECORD_KIND_FILE {
		t.Fatalf("file kind maps to %s, want QQ_RECORD_KIND_FILE", got)
	}
}
