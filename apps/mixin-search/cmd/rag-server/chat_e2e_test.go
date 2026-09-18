package main

import (
	"context"
	"net"
	"testing"
	"time"

	"mixin-search/internal/chat"
	"mixin-search/internal/chatindex"
	"mixin-search/internal/rag"
	"mixin-search/internal/security"
	grpcadapter "mixin-search/internal/transport/grpc"
	mixinsearchchatv1 "packages/gen/mixin-search/chat/v1"
	mixinsearchv1 "packages/gen/mixin-search/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

// TestChatCorpusEndToEndOverGRPC exercises the composition root's chat path with
// the same building blocks the binary uses: the chat corpus adapter over a
// dedicated collection, the chat transport adapter, and the capability
// interceptor. It runs in-process over a real gRPC connection, so it validates
// registration, authentication, role separation and the index/archive/retract
// round trip without needing a container runtime.
func TestChatCorpusEndToEndOverGRPC(t *testing.T) {
	const boundaryKey = "0123456789abcdef0123456789abcdef"
	ctx := context.Background()

	corpus, err := chatindex.New(ctx, chatindex.Config{
		VectorStore:   rag.NewMemoryStore(),
		ControlStore:  chat.NewMemoryControlStore(),
		StorageDomain: "go_web_chat_v1",
	})
	if err != nil {
		t.Fatalf("build chat corpus: %v", err)
	}
	t.Cleanup(func() { _ = corpus.Close() })
	corpus.StartProjectionReconciler(ctx)

	verifier, err := security.NewVerifier([]byte(boundaryKey), "go-web", "mixin-search")
	if err != nil {
		t.Fatalf("build verifier: %v", err)
	}
	authenticator, err := grpcadapter.NewAuthenticator(grpcadapter.AuthConfig{Verifier: verifier})
	if err != nil {
		t.Fatalf("build authenticator: %v", err)
	}
	chatServer, err := grpcadapter.NewChatServer(corpus.Service())
	if err != nil {
		t.Fatalf("build chat server: %v", err)
	}

	listener := bufconn.Listen(1024 * 1024)
	server := newGRPCServer(16<<20, &testRAGService{}, chatServer, authenticator, false)
	go func() {
		if err := server.Serve(listener); err != nil {
			t.Errorf("serve: %v", err)
		}
	}()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})

	connection, err := grpc.NewClient(
		"passthrough:///bufconn",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = connection.Close() })

	// Both services must be health-reported: the chat corpus registers only when
	// its collection and control namespace were built successfully.
	healthClient := healthpb.NewHealthClient(connection)
	for _, service := range []string{"", mixinsearchv1.RAGService_ServiceDesc.ServiceName, mixinsearchchatv1.ChatIndexService_ServiceDesc.ServiceName} {
		response, err := healthClient.Check(ctx, &healthpb.HealthCheckRequest{Service: service})
		if err != nil {
			t.Fatalf("health %q: %v", service, err)
		}
		if response.GetStatus() != healthpb.HealthCheckResponse_SERVING {
			t.Fatalf("health %q = %s, want SERVING", service, response.GetStatus())
		}
	}

	issuer, err := security.NewIssuer([]byte(boundaryKey), "go-web", "mixin-search", 5*time.Minute)
	if err != nil {
		t.Fatalf("build issuer: %v", err)
	}
	writerToken, err := issuer.Issue("py-agent-index", security.RoleChatIndexWriter, "", nil, nil)
	if err != nil {
		t.Fatalf("issue chat writer token: %v", err)
	}
	searcherToken, err := issuer.Issue("py-agent", security.RoleChatSearcher, "7", []string{"scope-group-42"}, []string{"group-42"})
	if err != nil {
		t.Fatalf("issue chat searcher token: %v", err)
	}
	documentToken, err := issuer.Issue("go-web-shadow-search", security.RoleSearcher, "42", []string{"space-a"}, nil)
	if err != nil {
		t.Fatalf("issue document searcher token: %v", err)
	}

	client := mixinsearchchatv1.NewChatIndexServiceClient(connection)

	// Index: write path, chat-index-writer only.
	if _, err := client.IndexConversationMessages(withToken(ctx, writerToken), &mixinsearchchatv1.IndexConversationMessagesRequest{
		OperationId: "chat-index-1", ConversationId: "group-42", OwnerScopeId: "scope-group-42",
		LifecycleRevision: 1,
		Messages: []*mixinsearchchatv1.ChatMessageInput{{
			MessageId: "m-1", SenderId: "qq-10001", SentAtUnixMs: 1_700_000_000_000, Content: "e2eneedle 部署记录",
		}},
	}); err != nil {
		t.Fatalf("index chat messages: %v", err)
	}

	// A message that is indexed but not archived must not be retrievable: that is
	// the three-state rule, and it has to hold through the real transport too.
	if hits := searchChat(t, client, ctx, searcherToken, "e2eneedle"); len(hits) != 0 {
		t.Fatalf("hits before archive = %d, want 0", len(hits))
	}

	if _, err := client.ArchiveConversation(withToken(ctx, writerToken), &mixinsearchchatv1.ArchiveConversationRequest{
		OperationId: "chat-archive-1", ConversationId: "group-42", ArchiveRevision: 1, LifecycleRevision: 1,
	}); err != nil {
		t.Fatalf("archive conversation: %v", err)
	}
	hits := searchChat(t, client, ctx, searcherToken, "e2eneedle")
	if len(hits) != 1 {
		t.Fatalf("hits after archive = %d, want 1", len(hits))
	}
	if hits[0].GetRef().GetConversationId() != "group-42" || hits[0].GetRef().GetMessageId() != "m-1" {
		t.Fatalf("hit = %+v", hits[0])
	}
	if hits[0].GetSenderId() != "qq-10001" || hits[0].GetSentAtUnixMs() != 1_700_000_000_000 {
		t.Fatalf("hit source fields = %+v", hits[0])
	}

	// Role separation through the real interceptor: a document capability must
	// not be able to search chat.
	if _, err := client.SearchChatMessages(withToken(ctx, documentToken), &mixinsearchchatv1.SearchChatMessagesRequest{
		Query: "e2eneedle", AllowedScopeIds: []string{"scope-group-42"},
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("document capability on chat search code = %s, want PermissionDenied", status.Code(err))
	}

	// Scope containment: asking for a scope the capability does not hold is
	// rejected rather than silently narrowed.
	if _, err := client.SearchChatMessages(withToken(ctx, searcherToken), &mixinsearchchatv1.SearchChatMessagesRequest{
		Query: "e2eneedle", AllowedScopeIds: []string{"scope-other"},
	}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("out-of-scope chat search code = %s, want PermissionDenied", status.Code(err))
	}

	// Retraction removes it from retrieval and leaves it indexed.
	if _, err := client.RetractMessage(withToken(ctx, writerToken), &mixinsearchchatv1.RetractMessageRequest{
		OperationId: "chat-retract-1", ConversationId: "group-42", MessageId: "m-1",
		RetractRevision: 1, LifecycleRevision: 1,
	}); err != nil {
		t.Fatalf("retract message: %v", err)
	}
	if hits := searchChat(t, client, ctx, searcherToken, "e2eneedle"); len(hits) != 0 {
		t.Fatalf("hits after retraction = %d, want 0", len(hits))
	}
	state, err := client.GetConversationIndexState(withToken(ctx, writerToken), &mixinsearchchatv1.GetConversationIndexStateRequest{
		ConversationId: "group-42",
	})
	if err != nil {
		t.Fatalf("conversation state: %v", err)
	}
	if !state.GetExists() || state.GetIndexedMessageCount() != 1 || state.GetRetractedMessageCount() != 1 {
		t.Fatalf("conversation state = %+v, want one indexed and retracted message", state)
	}
	if state.GetStatus() != mixinsearchchatv1.ConversationIndexStatus_CONVERSATION_INDEX_STATUS_ARCHIVED {
		t.Fatalf("status = %s, want ARCHIVED", state.GetStatus())
	}
}

func searchChat(
	t *testing.T,
	client mixinsearchchatv1.ChatIndexServiceClient,
	ctx context.Context,
	token string,
	query string,
) []*mixinsearchchatv1.ChatSearchHit {
	t.Helper()
	response, err := client.SearchChatMessages(withToken(ctx, token), &mixinsearchchatv1.SearchChatMessagesRequest{
		Query: query, AllowedScopeIds: []string{"scope-group-42"}, TopK: 5,
	})
	if err != nil {
		t.Fatalf("search chat messages: %v", err)
	}
	return response.GetHits()
}

func withToken(ctx context.Context, token string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
}
