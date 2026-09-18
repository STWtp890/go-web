package grpcadapter

import (
	"context"
	"errors"
	"testing"

	"mixin-search/internal/chat"
	mixinsearchchatv1 "packages/gen/mixin-search/chat/v1"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// chatServiceStub records the domain request the adapter produced, so the tests
// check the mapping rather than restating it.
type chatServiceStub struct {
	indexRequest   chat.IndexMessagesRequest
	searchRequest  chat.SearchMessagesRequest
	archiveRequest chat.ArchiveConversationRequest
	indexResponse  []chat.MessageState
	searchResponse chat.SearchMessagesResult
	stateResponse  chat.ConversationIndexState
	err            error
}

func (s *chatServiceStub) IndexMessages(_ context.Context, request chat.IndexMessagesRequest) ([]chat.MessageState, error) {
	s.indexRequest = request
	return s.indexResponse, s.err
}

func (s *chatServiceStub) ArchiveConversation(_ context.Context, request chat.ArchiveConversationRequest) (chat.ArchiveResult, error) {
	s.archiveRequest = request
	return chat.ArchiveResult{Status: chat.StatusArchived, ArchiveRevision: request.ArchiveRevision, LifecycleRevision: request.LifecycleRevision}, s.err
}

func (s *chatServiceStub) UpdateConversationAccess(context.Context, chat.UpdateConversationAccessRequest) (chat.AccessState, error) {
	return chat.AccessState{}, s.err
}

func (s *chatServiceStub) RetractMessage(context.Context, chat.RetractMessageRequest) (chat.RetractResult, error) {
	return chat.RetractResult{}, s.err
}

func (s *chatServiceStub) DeleteConversation(context.Context, chat.DeleteConversationRequest) (chat.DeleteResult, error) {
	return chat.DeleteResult{}, s.err
}

func (s *chatServiceStub) GetConversationIndexState(context.Context, chat.GetConversationIndexStateRequest) (chat.ConversationIndexState, error) {
	return s.stateResponse, s.err
}

func (s *chatServiceStub) SearchMessages(_ context.Context, request chat.SearchMessagesRequest) (chat.SearchMessagesResult, error) {
	s.searchRequest = request
	return s.searchResponse, s.err
}

func TestChatAdapterMapsIndexRequestAndStates(t *testing.T) {
	t.Parallel()

	stub := &chatServiceStub{indexResponse: []chat.MessageState{{
		ConversationID: "group-42", MessageID: "m-1", OwnerScopeID: "scope-group-42",
		SenderID: "sender", SentAtUnixMs: 1_700_000_000_000, ArchiveRevision: 1, AccessRevision: 2,
		LifecycleRevision: 3, RetractRevision: 4, Retracted: true, ChunkCount: 2, ContentSHA256: "digest",
	}}}
	server, err := NewChatServer(stub)
	if err != nil {
		t.Fatalf("new chat server: %v", err)
	}

	response, err := server.IndexConversationMessages(context.Background(), &mixinsearchchatv1.IndexConversationMessagesRequest{
		OperationId: "op-1", ConversationId: "group-42", OwnerScopeId: "scope-group-42", LifecycleRevision: 1,
		Messages: []*mixinsearchchatv1.ChatMessageInput{{
			MessageId: "m-1", SenderId: "sender", SentAtUnixMs: 1_700_000_000_000,
			Content: "content", ContentSha256: "digest", Metadata: map[string]string{"channel": "qq"},
		}},
	})
	if err != nil {
		t.Fatalf("index conversation messages: %v", err)
	}
	if len(stub.indexRequest.Messages) != 1 {
		t.Fatalf("mapped messages = %d, want 1", len(stub.indexRequest.Messages))
	}
	mapped := stub.indexRequest.Messages[0]
	if mapped.MessageID != "m-1" || mapped.SenderID != "sender" || mapped.Content != "content" ||
		mapped.ContentSHA256 != "digest" || mapped.Metadata["channel"] != "qq" {
		t.Fatalf("mapped message = %+v", mapped)
	}
	if stub.indexRequest.ConversationID != "group-42" || stub.indexRequest.OwnerScopeID != "scope-group-42" {
		t.Fatalf("mapped request = %+v", stub.indexRequest)
	}

	if len(response.GetStates()) != 1 {
		t.Fatalf("states = %d, want 1", len(response.GetStates()))
	}
	state := response.GetStates()[0]
	if state.GetRef().GetConversationId() != "group-42" || state.GetRef().GetMessageId() != "m-1" ||
		!state.GetRetracted() || state.GetRetractRevision() != 4 || state.GetChunkCount() != 2 {
		t.Fatalf("rendered state = %+v", state)
	}
}

func TestChatAdapterMapsSearchRequestAndHits(t *testing.T) {
	t.Parallel()

	stub := &chatServiceStub{searchResponse: chat.SearchMessagesResult{
		Query: "normalized", Truncated: true,
		Hits: []chat.MessageHit{{
			ConversationID: "group-42", MessageID: "m-1", OwnerScopeID: "scope-group-42",
			SenderID: "sender", SentAtUnixMs: 1_700_000_000_000, Position: 3, Snippet: "snippet",
			ContentSHA256: "digest", Score: 0.5,
		}},
	}}
	server, err := NewChatServer(stub)
	if err != nil {
		t.Fatalf("new chat server: %v", err)
	}

	response, err := server.SearchChatMessages(context.Background(), &mixinsearchchatv1.SearchChatMessagesRequest{
		Query: "query", AllowedScopeIds: []string{"scope-group-42"},
		AllowedConversationIds: []string{"group-42"}, SentAfterUnixMs: 10, SentBeforeUnixMs: 20, TopK: 7,
	})
	if err != nil {
		t.Fatalf("search chat messages: %v", err)
	}
	request := stub.searchRequest
	if request.Query != "query" || request.TopK != 7 || request.SentAfterUnixMs != 10 || request.SentBeforeUnixMs != 20 ||
		len(request.AllowedScopeIDs) != 1 || len(request.AllowedConversationIDs) != 1 {
		t.Fatalf("mapped search request = %+v", request)
	}
	if response.GetQuery() != "normalized" || !response.GetTruncated() || len(response.GetHits()) != 1 {
		t.Fatalf("mapped response = %+v", response)
	}
	hit := response.GetHits()[0]
	if hit.GetRef().GetConversationId() != "group-42" || hit.GetRef().GetMessageId() != "m-1" ||
		hit.GetPosition() != 3 || hit.GetSnippet() != "snippet" || hit.GetRrfScore() != 0.5 {
		t.Fatalf("rendered hit = %+v", hit)
	}
}

func TestChatAdapterRejectsNilRequests(t *testing.T) {
	t.Parallel()

	server, err := NewChatServer(&chatServiceStub{})
	if err != nil {
		t.Fatalf("new chat server: %v", err)
	}
	if _, err := server.IndexConversationMessages(context.Background(), nil); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("nil index request code = %s, want InvalidArgument", status.Code(err))
	}
	if _, err := server.SearchChatMessages(context.Background(), nil); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("nil search request code = %s, want InvalidArgument", status.Code(err))
	}
	if _, err := NewChatServer(nil); err == nil {
		t.Fatal("NewChatServer without a service succeeded, want error")
	}
}

// TestChatAdapterMapsDomainErrors locks the status codes callers retry on: a
// capability or lifecycle failure must never look retryable, and an unavailable
// collection must.
func TestChatAdapterMapsDomainErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		want codes.Code
	}{
		{"invalid input", chat.ErrInvalidInput, codes.InvalidArgument},
		{"not found", chat.ErrNotFound, codes.NotFound},
		{"conflict", chat.ErrConflict, codes.FailedPrecondition},
		{"stale lifecycle", chat.ErrStaleLifecycle, codes.FailedPrecondition},
		{"stale archive", chat.ErrStaleArchive, codes.FailedPrecondition},
		{"stale access", chat.ErrStaleAccess, codes.FailedPrecondition},
		{"stale retract", chat.ErrStaleRetract, codes.FailedPrecondition},
		{"control conflict", chat.ErrControlStoreConflict, codes.Aborted},
		{"control unavailable", chat.ErrControlStoreUnavailable, codes.Unavailable},
		{"projection unavailable", chat.ErrProjectionUnavailable, codes.Unavailable},
		{"cancelled", context.Canceled, codes.Canceled},
		{"deadline", context.DeadlineExceeded, codes.DeadlineExceeded},
		{"unknown", errors.New("boom"), codes.Internal},
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			if got := status.Code(mapChatServiceError(testCase.err)); got != testCase.want {
				t.Fatalf("code = %s, want %s", got, testCase.want)
			}
		})
	}
}
