package application

// PostgreSQL integration tests for the scoped record-state lookup. They run
// against the real development database named by QQ_SEARCH_TEST_DSN and fail -
// never skip - when it cannot be reached.
//
// GetQQRecordState is the one query RPC that addresses a single record by id, so
// it is the one that could be used to probe whether another bot or conversation
// holds a given record id. These tests pin the answer: outside the granted
// channel scope the lookup is indistinguishable from a lookup of a record that
// does not exist, an empty grant finds nothing at all, and the scope really is
// applied inside each model's own statement.

import (
	"context"
	"testing"

	qqsearchv1 "packages/gen/qqsearch/v1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// otherGroupID is a group the test's capability is never granted for.
const otherGroupID = "1000"

// otherBotID is a bot the test's capability is never granted for.
const otherBotID = "10002"

// messageEventInBotConversation builds a message_upsert for another bot's group.
func messageEventInBotConversation(botID, groupID, recordID, text string, revision uint64, name string) *qqsearchv1.IndexQQSourceEventRequest {
	request := messageEventFor(recordID, text, revision, name)
	request.BotId = botID
	request.ConversationId = "qq:" + botID + ":group:" + groupID
	request.ExternalGroupId = groupID
	return request
}

// fileEventInGroup builds a file_upsert for an arbitrary bot and group.
func fileEventInGroup(botID, groupID, recordID, fileName string, revision uint64, name string) *qqsearchv1.IndexQQSourceEventRequest {
	request := fileEventFor(recordID, fileName, revision, name)
	request.BotId = botID
	request.ConversationId = "qq:" + botID + ":group:" + groupID
	request.ExternalGroupId = groupID
	return request
}

// messageRecallInGroup builds a message_recalled for one record in an arbitrary
// group: a recall names the conversation of the record it withdraws, so it
// cannot reuse the fixture's default one.
func messageRecallInGroup(groupID, recordID string, revision uint64, name string) *qqsearchv1.IndexQQSourceEventRequest {
	request := recallEventFor(RecordKindMessage, recordID, revision, name)
	request.ConversationId = "qq:" + testBotID + ":group:" + groupID
	request.ExternalGroupId = groupID
	return request
}

// lookupState performs one scoped record-state lookup, failing on a transport
// error.
func lookupState(t *testing.T, service *Service, granted *qqsearchv1.QQChannelScope, request *qqsearchv1.GetQQRecordStateRequest) *qqsearchv1.GetQQRecordStateResponse {
	t.Helper()
	response, err := service.GetRecordState(context.Background(), granted, request)
	if err != nil {
		t.Fatalf("GetRecordState(%s %s): %v", request.GetKind(), request.GetRecordId(), err)
	}
	return response
}

// requireHidden fails unless the lookup answered exactly like a lookup of a
// record that does not exist: exists=false and no state at all.
func requireHidden(t *testing.T, what string, response *qqsearchv1.GetQQRecordStateResponse) {
	t.Helper()
	if response.GetExists() {
		t.Fatalf("%s: exists=true, want exists=false", what)
	}
	if response.GetState() != nil {
		t.Fatalf("%s: state=%+v, want no state for a hidden record", what, response.GetState())
	}
}

// requireRowCount asserts the row really is stored, so "hidden" is a scope
// decision and not an accidentally missing record.
func requireRowCount(t *testing.T, pool querier, table, recordID string, want int) {
	t.Helper()
	var count int
	if err := pool.QueryRow(context.Background(),
		"SELECT count(*) FROM qq_search."+table+" WHERE record_id = $1", recordID).Scan(&count); err != nil {
		t.Fatalf("count rows in %s for %s: %v", table, recordID, err)
	}
	if count != want {
		t.Fatalf("stored %s rows for %s = %d, want %d", table, recordID, count, want)
	}
}

func messageStateRequest(recordID string, scope *qqsearchv1.QQChannelScope) *qqsearchv1.GetQQRecordStateRequest {
	return &qqsearchv1.GetQQRecordStateRequest{
		Kind:     qqsearchv1.QQRecordKind_QQ_RECORD_KIND_MESSAGE,
		RecordId: recordID,
		Scope:    scope,
	}
}

func fileStateRequest(recordID string, scope *qqsearchv1.QQChannelScope) *qqsearchv1.GetQQRecordStateRequest {
	return &qqsearchv1.GetQQRecordStateRequest{
		Kind:     qqsearchv1.QQRecordKind_QQ_RECORD_KIND_FILE,
		RecordId: recordID,
		Scope:    scope,
	}
}

func TestIntegrationGetRecordStateIsBlindOutsideTheGrantedChannelScope(t *testing.T) {
	service, pool := openTestDatabase(t)
	ctx := context.Background()
	token := uniqueToken()

	inScopeMessage := nextRecordID(t, "msg")
	otherGroupMessage := nextRecordID(t, "msg")
	otherBotMessage := nextRecordID(t, "msg")
	inScopeFile := nextRecordID(t, "file")
	otherGroupFile := nextRecordID(t, "file")

	mustApply(t, service, messageEventFor(inScopeMessage, "in scope "+token, 1, "state-in-"+inScopeMessage))
	mustApply(t, service, messageEventInConversation(otherGroupID, otherGroupMessage, "other group "+token, 1, "state-group-"+otherGroupMessage))
	mustApply(t, service, messageEventInBotConversation(otherBotID, testGroupID, otherBotMessage, "other bot "+token, 1, "state-bot-"+otherBotMessage))
	mustApply(t, service, fileEventFor(inScopeFile, "in-scope-"+token+".pdf", 1, "state-file-in-"+inScopeFile))
	mustApply(t, service, fileEventInGroup(testBotID, otherGroupID, otherGroupFile, "other-group-"+token+".pdf", 1, "state-file-group-"+otherGroupFile))

	// Every row exists: the lookups below are refused by the scope, not by an
	// absent record.
	requireRowCount(t, pool, "qq_messages", inScopeMessage, 1)
	requireRowCount(t, pool, "qq_messages", otherGroupMessage, 1)
	requireRowCount(t, pool, "qq_messages", otherBotMessage, 1)
	requireRowCount(t, pool, "qq_files", otherGroupFile, 1)

	granted := grantGroupScope(testGroupID) // bot 10001, group 999 only

	// In scope: reported as indexed.
	visible := lookupState(t, service, granted, messageStateRequest(inScopeMessage, nil))
	if !visible.GetExists() || visible.GetState().GetStatus() != qqsearchv1.QQRecordStatus_QQ_RECORD_STATUS_INDEXED {
		t.Fatalf("in-scope message = %+v, want exists with INDEXED", visible)
	}

	// Out of scope by conversation: hidden.
	requireHidden(t, "message in another group", lookupState(t, service, granted, messageStateRequest(otherGroupMessage, nil)))
	// Out of scope by bot: hidden even though the conversation id names the
	// granted group.
	requireHidden(t, "message in another bot", lookupState(t, service, granted, messageStateRequest(otherBotMessage, nil)))
	// Out of scope by conversation, file model: hidden too.
	requireHidden(t, "file in another group", lookupState(t, service, granted, fileStateRequest(otherGroupFile, nil)))

	// The out-of-scope answer is exactly the answer for a record that was never
	// indexed: no caller can tell "not yours" from "not there".
	missing := lookupState(t, service, granted, messageStateRequest(nextRecordID(t, "msg"), nil))
	outOfScope := lookupState(t, service, granted, messageStateRequest(otherGroupMessage, nil))
	if outOfScope.String() != missing.String() {
		t.Fatalf("out-of-scope response %q differs from the missing-record response %q",
			outOfScope.String(), missing.String())
	}

	// A request that names something outside the grant is refused as a whole,
	// for both models, instead of being trimmed into a hidden answer.
	for name, request := range map[string]*qqsearchv1.GetQQRecordStateRequest{
		"message, another bot": messageStateRequest(otherGroupMessage, &qqsearchv1.QQChannelScope{BotIds: []string{otherBotID}}),
		"message, another group": messageStateRequest(otherGroupMessage, &qqsearchv1.QQChannelScope{
			ConversationIds: []string{"qq:" + testBotID + ":group:" + otherGroupID},
		}),
		"file, another group": fileStateRequest(otherGroupFile, &qqsearchv1.QQChannelScope{
			ExternalGroupIds: []string{otherGroupID},
		}),
	} {
		_, err := service.GetRecordState(ctx, granted, request)
		if status.Code(err) != codes.PermissionDenied {
			t.Fatalf("%s: got %v, want PERMISSION_DENIED", name, err)
		}
	}

	// The request narrows the query, not just the grant: with both groups granted
	// the wide capability sees the other group's record, and the same capability
	// narrowed to this group does not.
	wide := grantGroupScope(testGroupID, otherGroupID)
	if wideResponse := lookupState(t, service, wide, messageStateRequest(otherGroupMessage, nil)); !wideResponse.GetExists() {
		t.Fatalf("the record is inside the wide grant but was reported as missing: %+v", wideResponse)
	}
	narrowed := messageStateRequest(otherGroupMessage, &qqsearchv1.QQChannelScope{
		ConversationIds: []string{"qq:" + testBotID + ":group:" + testGroupID},
	})
	requireHidden(t, "wide grant narrowed to this group", lookupState(t, service, wide, narrowed))
}

func TestIntegrationGetRecordStateWithAnEmptyGrantFindsNothing(t *testing.T) {
	service, pool := openTestDatabase(t)
	ctx := context.Background()
	token := uniqueToken()
	messageID := nextRecordID(t, "msg")
	fileID := nextRecordID(t, "file")

	mustApply(t, service, messageEventFor(messageID, "empty grant "+token, 1, "empty-msg-"+messageID))
	mustApply(t, service, fileEventFor(fileID, "empty-grant-"+token+".pdf", 1, "empty-file-"+fileID))
	requireRowCount(t, pool, "qq_messages", messageID, 1)
	requireRowCount(t, pool, "qq_files", fileID, 1)

	// An empty grant is "no conversations". It must never degrade into "all
	// conversations", which would make this lookup a probe for every record.
	empty := &qqsearchv1.QQChannelScope{}
	requireHidden(t, "empty grant, message", lookupState(t, service, empty, messageStateRequest(messageID, nil)))
	requireHidden(t, "empty grant, file", lookupState(t, service, empty, fileStateRequest(fileID, nil)))

	// The same empty grant cannot be widened by naming identifiers in the
	// request: that is refused, not answered.
	grant := grantGroupScope(testGroupID)
	for name, request := range map[string]*qqsearchv1.GetQQRecordStateRequest{
		"message": messageStateRequest(messageID, grant),
		"file":    fileStateRequest(fileID, grant),
	} {
		_, err := service.GetRecordState(ctx, empty, request)
		if status.Code(err) != codes.PermissionDenied {
			t.Fatalf("empty grant with a named %s scope: got %v, want PERMISSION_DENIED", name, err)
		}
	}
}

func TestIntegrationGetRecordStateReportsAnInScopeRecall(t *testing.T) {
	service, pool := openTestDatabase(t)
	token := uniqueToken()
	messageID := nextRecordID(t, "msg")
	fileID := nextRecordID(t, "file")
	otherGroupMessage := nextRecordID(t, "msg")

	mustApply(t, service, messageEventFor(messageID, "recalled "+token, 1, "state-recall-msg-1-"+messageID))
	mustApply(t, service, recallEventFor(RecordKindMessage, messageID, 2, "state-recall-msg-2-"+messageID))
	mustApply(t, service, fileEventFor(fileID, "recalled-"+token+".pdf", 1, "state-recall-file-1-"+fileID))
	mustApply(t, service, recallEventFor(RecordKindFile, fileID, 2, "state-recall-file-2-"+fileID))
	mustApply(t, service, messageEventInConversation(otherGroupID, otherGroupMessage, "outside "+token, 1, "state-recall-out-"+otherGroupMessage))
	mustApply(t, service, messageRecallInGroup(otherGroupID, otherGroupMessage, 2, "state-recall-out-2-"+otherGroupMessage))

	granted := grantGroupScope(testGroupID)

	// Recalled in scope: still reportable, with the recall state and revision.
	messageState := lookupState(t, service, granted, messageStateRequest(messageID, nil))
	if !messageState.GetExists() || messageState.GetState().GetStatus() != qqsearchv1.QQRecordStatus_QQ_RECORD_STATUS_RECALLED {
		t.Fatalf("recalled message = %+v, want exists with RECALLED", messageState)
	}
	if got := messageState.GetState().GetRecordRevision(); got != 2 {
		t.Fatalf("recalled message revision = %d, want 2", got)
	}
	fileState := lookupState(t, service, granted, fileStateRequest(fileID, nil))
	if !fileState.GetExists() || fileState.GetState().GetStatus() != qqsearchv1.QQRecordStatus_QQ_RECORD_STATUS_RECALLED {
		t.Fatalf("recalled file = %+v, want exists with RECALLED", fileState)
	}
	if got := fileState.GetState().GetRecordRevision(); got != 2 {
		t.Fatalf("recalled file revision = %d, want 2", got)
	}

	// Recalled outside the scope: hidden, exactly like a missing record. The
	// recall state is not a side channel either.
	requireHidden(t, "recalled message outside the scope",
		lookupState(t, service, granted, messageStateRequest(otherGroupMessage, nil)))

	// The kind selector still selects one model: inside the same grant a file id
	// looked up as a message is missing.
	requireHidden(t, "file id looked up as a message",
		lookupState(t, service, granted, messageStateRequest(fileID, nil)))
	requireHidden(t, "message id looked up as a file",
		lookupState(t, service, granted, fileStateRequest(messageID, nil)))

	requireRowCount(t, pool, "qq_messages", otherGroupMessage, 1)
}
