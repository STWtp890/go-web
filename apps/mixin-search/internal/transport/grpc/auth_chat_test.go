package grpcadapter

import (
	"testing"

	"mixin-search/internal/security"
	mixinsearchchatv1 "packages/gen/mixin-search/chat/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestChatSearchEnforcesScopeContainment is the chat counterpart of the document
// scope rule: a request may only ask for less than it was granted.
func TestChatSearchEnforcesScopeContainment(t *testing.T) {
	t.Parallel()

	authenticator, issuer, sink := newTestAuthenticator(t)
	token, err := issuer.Issue("py-agent", security.RoleChatSearcher, "7",
		[]string{"scope-group-42"}, []string{"group-42"})
	if err != nil {
		t.Fatalf("issue chat searcher token: %v", err)
	}
	info := &grpc.UnaryServerInfo{FullMethod: mixinsearchchatv1.ChatIndexService_SearchChatMessages_FullMethodName}

	allowed := []*mixinsearchchatv1.SearchChatMessagesRequest{
		{Query: "x", AllowedScopeIds: []string{"scope-group-42"}},
		{Query: "x", AllowedConversationIds: []string{"group-42"}},
		{Query: "x"},
	}
	for index, request := range allowed {
		spy := &handlerSpy{}
		if _, err := authenticator.UnaryInterceptor(incomingContext(token), request, info, spy.handler); err != nil {
			t.Fatalf("in-scope chat search %d failed: %v", index, err)
		}
		if !spy.called {
			t.Fatalf("in-scope chat search %d never reached the handler", index)
		}
	}

	denied := []*mixinsearchchatv1.SearchChatMessagesRequest{
		{Query: "x", AllowedScopeIds: []string{"scope-other"}},
		{Query: "x", AllowedConversationIds: []string{"group-other"}},
		{Query: "x", AllowedScopeIds: []string{"scope-group-42", "scope-other"}},
	}
	for index, request := range denied {
		spy := &handlerSpy{}
		_, err := authenticator.UnaryInterceptor(incomingContext(token), request, info, spy.handler)
		if status.Code(err) != codes.PermissionDenied {
			t.Fatalf("out-of-scope chat search %d code = %s, want PermissionDenied", index, status.Code(err))
		}
		if spy.called {
			t.Fatalf("out-of-scope chat search %d reached the handler", index)
		}
	}
	if sink.last().Outcome != security.OutcomeDenied {
		t.Fatalf("audit outcome = %q, want denied", sink.last().Outcome)
	}
}

// TestChatAuditCountsRequestedScopesTheWayItCountsGrantedOnes keeps the two
// sizes in the audit record comparable: a client that repeats a scope or pads
// it with whitespace asked for one scope, and the record must not read as an
// attempted over-reach.
func TestChatAuditCountsRequestedScopesTheWayItCountsGrantedOnes(t *testing.T) {
	t.Parallel()

	authenticator, issuer, sink := newTestAuthenticator(t)
	token, err := issuer.Issue("py-agent", security.RoleChatSearcher, "7",
		[]string{"scope-group-42"}, []string{"group-42"})
	if err != nil {
		t.Fatalf("issue chat searcher token: %v", err)
	}
	spy := &handlerSpy{}
	if _, err := authenticator.UnaryInterceptor(
		incomingContext(token),
		&mixinsearchchatv1.SearchChatMessagesRequest{
			Query:           "x",
			AllowedScopeIds: []string{" scope-group-42 ", "scope-group-42", ""},
		},
		&grpc.UnaryServerInfo{FullMethod: mixinsearchchatv1.ChatIndexService_SearchChatMessages_FullMethodName},
		spy.handler,
	); err != nil {
		t.Fatalf("chat search with a repeated scope failed: %v", err)
	}
	if !spy.called {
		t.Fatal("chat search with a repeated scope never reached the handler")
	}
	record := sink.last()
	if record.RequestedScope != 1 || record.GrantedScope != 2 {
		t.Fatalf("audit scope = requested %d granted %d, want 1 and 2", record.RequestedScope, record.GrantedScope)
	}
}

// TestChatAndDocumentCapabilitiesAreNotInterchangeable is the authorization half
// of corpus isolation: holding a document capability must not let a caller touch
// the chat corpus, and the reverse.
func TestChatAndDocumentCapabilitiesAreNotInterchangeable(t *testing.T) {
	t.Parallel()

	authenticator, issuer, _ := newTestAuthenticator(t)
	documentSearcher, err := issuer.Issue("go-web-shadow-search", security.RoleSearcher, "42", []string{"space-a"}, nil)
	if err != nil {
		t.Fatalf("issue document searcher token: %v", err)
	}
	documentWriter, err := issuer.Issue("go-web-index-worker", security.RoleIndexWriter, "", nil, nil)
	if err != nil {
		t.Fatalf("issue document writer token: %v", err)
	}
	chatSearcher, err := issuer.Issue("py-agent", security.RoleChatSearcher, "7", []string{"scope-a"}, nil)
	if err != nil {
		t.Fatalf("issue chat searcher token: %v", err)
	}

	cases := []struct {
		name   string
		token  string
		method string
		body   any
		code   codes.Code
	}{
		{
			name:   "document searcher cannot search chat",
			token:  documentSearcher,
			method: mixinsearchchatv1.ChatIndexService_SearchChatMessages_FullMethodName,
			body:   &mixinsearchchatv1.SearchChatMessagesRequest{Query: "x"},
			code:   codes.PermissionDenied,
		},
		{
			name:   "document writer cannot index chat",
			token:  documentWriter,
			method: mixinsearchchatv1.ChatIndexService_IndexConversationMessages_FullMethodName,
			body:   &mixinsearchchatv1.IndexConversationMessagesRequest{OperationId: "op", ConversationId: "c"},
			code:   codes.PermissionDenied,
		},
		{
			name:   "chat searcher cannot delete a conversation",
			token:  chatSearcher,
			method: mixinsearchchatv1.ChatIndexService_DeleteConversation_FullMethodName,
			body:   &mixinsearchchatv1.DeleteConversationRequest{OperationId: "op", ConversationId: "c"},
			code:   codes.PermissionDenied,
		},
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			spy := &handlerSpy{}
			_, err := authenticator.UnaryInterceptor(
				incomingContext(testCase.token), testCase.body,
				&grpc.UnaryServerInfo{FullMethod: testCase.method}, spy.handler,
			)
			if status.Code(err) != testCase.code {
				t.Fatalf("code = %s (err=%v), want %s", status.Code(err), err, testCase.code)
			}
			if spy.called {
				t.Fatal("the RPC body ran for a capability from the other corpus")
			}
		})
	}
}

// TestChatOpsMayOnlyReadState keeps the write/read split inside the chat corpus.
func TestChatOpsMayOnlyReadState(t *testing.T) {
	t.Parallel()

	authenticator, issuer, _ := newTestAuthenticator(t)
	token, err := issuer.Issue("py-agent-ops", security.RoleChatOps, "", nil, nil)
	if err != nil {
		t.Fatalf("issue chat ops token: %v", err)
	}

	spy := &handlerSpy{}
	if _, err := authenticator.UnaryInterceptor(
		incomingContext(token),
		&mixinsearchchatv1.GetConversationIndexStateRequest{ConversationId: "c"},
		&grpc.UnaryServerInfo{FullMethod: mixinsearchchatv1.ChatIndexService_GetConversationIndexState_FullMethodName},
		spy.handler,
	); err != nil {
		t.Fatalf("chat ops state read failed: %v", err)
	}

	spy = &handlerSpy{}
	_, err = authenticator.UnaryInterceptor(
		incomingContext(token),
		&mixinsearchchatv1.ArchiveConversationRequest{OperationId: "op", ConversationId: "c"},
		&grpc.UnaryServerInfo{FullMethod: mixinsearchchatv1.ChatIndexService_ArchiveConversation_FullMethodName},
		spy.handler,
	)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("chat ops archive code = %s, want PermissionDenied", status.Code(err))
	}
}
