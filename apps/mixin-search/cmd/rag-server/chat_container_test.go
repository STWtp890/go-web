package main

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"mixin-search/internal/security"
	mixinsearchchatv1 "packages/gen/mixin-search/chat/v1"
	mixinsearchv1 "packages/gen/mixin-search/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

// TestChatCorpusAgainstADeployedStack is the container-level half of P3.3's
// acceptance: the in-process test next door proves the wiring, and this one
// proves the two dependencies that wiring cannot stand in for - the chat
// control table in PostgreSQL and the chat collection in Qdrant.
//
// It is gated because it needs a running stack:
//
//	CHAT_CONTAINER_INTEGRATION=1 \
//	CHAT_CONTAINER_ADDRESS=127.0.0.1:19090 \
//	CHAT_CONTAINER_CAPABILITY_KEY_FILE=deployments/secrets/mixin_search_capability.key \
//	go test ./cmd/rag-server -run TestChatCorpusAgainstADeployedStack -v
//
// The capability key is the boundary key the deployed container verifies with,
// so this test also proves that a service the composition root started accepts
// credentials the same composition root would mint.
func TestChatCorpusAgainstADeployedStack(t *testing.T) {
	if os.Getenv("CHAT_CONTAINER_INTEGRATION") != "1" {
		t.Skip("set CHAT_CONTAINER_INTEGRATION=1 to run against a deployed stack")
	}
	address := os.Getenv("CHAT_CONTAINER_ADDRESS")
	if address == "" {
		t.Fatal("CHAT_CONTAINER_ADDRESS is required, for example 127.0.0.1:19090")
	}
	keyPath := os.Getenv("CHAT_CONTAINER_CAPABILITY_KEY_FILE")
	if keyPath == "" {
		t.Fatal("CHAT_CONTAINER_CAPABILITY_KEY_FILE is required (the deployed boundary key)")
	}
	key, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("read boundary key: %v", err)
	}

	issuer, err := security.NewIssuer(trimBoundaryKey(key), "go-web", security.AudienceDocuments, 5*time.Minute)
	if err != nil {
		t.Fatalf("build issuer: %v", err)
	}
	chatIssuer, err := security.NewIssuer(trimBoundaryKey(key), "go-web", security.AudienceChat, 5*time.Minute)
	if err != nil {
		t.Fatalf("build chat issuer: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial %s: %v", address, err)
	}
	defer func() { _ = connection.Close() }()

	for _, service := range []string{
		mixinsearchv1.RAGService_ServiceDesc.ServiceName,
		mixinsearchchatv1.ChatIndexService_ServiceDesc.ServiceName,
	} {
		response, err := healthpb.NewHealthClient(connection).Check(ctx, &healthpb.HealthCheckRequest{Service: service})
		if err != nil {
			t.Fatalf("health %q: %v", service, err)
		}
		if response.GetStatus() != healthpb.HealthCheckResponse_SERVING {
			t.Fatalf("health %q = %s, want SERVING", service, response.GetStatus())
		}
	}

	// Every run uses its own conversation and operation ids: the deployed control
	// plane keeps tombstones and an idempotency ledger across runs.
	run := fmt.Sprintf("%d", time.Now().UnixNano())
	conversationID := "container-" + run
	scopeID := "scope-" + run
	needle := "containerneedle" + run

	writerToken := mustIssue(t, chatIssuer, "py-agent-index", security.RoleChatIndexWriter, "", nil, nil)
	searcherToken := mustIssue(t, chatIssuer, "py-agent", security.RoleChatSearcher, "7", []string{scopeID}, []string{conversationID})
	documentToken := mustIssue(t, issuer, "go-web-shadow-search", security.RoleSearcher, "42", []string{"space-a"}, nil)
	// A chat role minted with the document audience: the deployed service must
	// refuse it before looking at the role table.
	mixedToken := mustIssue(t, issuer, "py-agent", security.RoleChatSearcher, "7", []string{scopeID}, []string{conversationID})

	chat := mixinsearchchatv1.NewChatIndexServiceClient(connection)
	indexRequest := &mixinsearchchatv1.IndexConversationMessagesRequest{
		OperationId: "container-index-" + run, ConversationId: conversationID, OwnerScopeId: scopeID,
		LifecycleRevision: 1,
		Messages: []*mixinsearchchatv1.ChatMessageInput{{
			MessageId: "m-1", SenderId: "qq-10001", SentAtUnixMs: 1_700_000_000_000, Content: needle,
		}},
	}
	if _, err := chat.IndexConversationMessages(withToken(ctx, writerToken), indexRequest); err != nil {
		t.Fatalf("index chat messages: %v", err)
	}
	// Indexing is not retrieval: the conversation is not archived yet.
	if hits := searchContainerChat(t, chat, ctx, searcherToken, needle, scopeID); len(hits) != 0 {
		t.Fatalf("hits before archive = %d, want 0", len(hits))
	}
	if _, err := chat.ArchiveConversation(withToken(ctx, writerToken), &mixinsearchchatv1.ArchiveConversationRequest{
		OperationId: "container-archive-" + run, ConversationId: conversationID,
		ArchiveRevision: 1, LifecycleRevision: 1,
	}); err != nil {
		t.Fatalf("archive conversation: %v", err)
	}
	// This is the assertion the container gate exists for: the vectors really
	// reached the chat collection, and the control plane really reads its own
	// PostgreSQL state back.
	hits := searchContainerChat(t, chat, ctx, searcherToken, needle, scopeID)
	if len(hits) != 1 {
		t.Fatalf("hits after archive = %d, want 1", len(hits))
	}
	if hits[0].GetRef().GetConversationId() != conversationID || hits[0].GetRef().GetMessageId() != "m-1" {
		t.Fatalf("hit = %+v", hits[0])
	}

	// Corpus isolation through the real transport: neither a document capability
	// nor a chat role minted with the document audience may reach the chat
	// service. (The document-side check below is only a smoke test here: at this
	// point in the gate the document corpus is still empty, so it shows that a
	// chat-only needle does not surface through SearchDocuments, not that documents
	// are unaffected. The alias mapping assertions above and the document steps
	// later in the gate are what carry that weight.)
	for name, token := range map[string]string{"document capability": documentToken, "mixed corpus credential": mixedToken} {
		if _, err := chat.SearchChatMessages(withToken(ctx, token), &mixinsearchchatv1.SearchChatMessagesRequest{
			Query: needle, AllowedScopeIds: []string{scopeID},
		}); status.Code(err) != codes.Unauthenticated {
			t.Fatalf("%s on chat search code = %s, want Unauthenticated", name, status.Code(err))
		}
	}
	documents := mixinsearchv1.NewRAGServiceClient(connection)
	documentResponse, err := documents.SearchDocuments(withToken(ctx, documentToken), &mixinsearchv1.SearchDocumentsRequest{
		Query: needle, AllowedSpaceIds: []string{"space-a"}, TopK: 5,
	})
	if err != nil {
		t.Fatalf("document search: %v", err)
	}
	if len(documentResponse.GetHits()) != 0 {
		t.Fatalf("document corpus returned %d hits for a chat-only message", len(documentResponse.GetHits()))
	}

	// Retraction keeps the message indexed and counted while removing it from
	// retrieval - the three-state rule, now against the real stores.
	if _, err := chat.RetractMessage(withToken(ctx, writerToken), &mixinsearchchatv1.RetractMessageRequest{
		OperationId: "container-retract-" + run, ConversationId: conversationID, MessageId: "m-1",
		RetractRevision: 1, LifecycleRevision: 1,
	}); err != nil {
		t.Fatalf("retract message: %v", err)
	}
	if hits := searchContainerChat(t, chat, ctx, searcherToken, needle, scopeID); len(hits) != 0 {
		t.Fatalf("hits after retraction = %d, want 0", len(hits))
	}
	state, err := chat.GetConversationIndexState(withToken(ctx, writerToken), &mixinsearchchatv1.GetConversationIndexStateRequest{
		ConversationId: conversationID,
	})
	if err != nil {
		t.Fatalf("conversation state: %v", err)
	}
	if !state.GetExists() || state.GetIndexedMessageCount() != 1 || state.GetRetractedMessageCount() != 1 {
		t.Fatalf("conversation state = %+v, want one indexed and retracted message", state)
	}

	// Idempotency through the deployed control plane. The assertion is what
	// distinguishes a ledger replay from the "already indexed" shortcut: the ledger
	// returns the *first* response, which still says the message was not retracted,
	// while recomputing from current state would report the retraction. So this
	// fails if the ledger entry is missing or ignored.
	replayed, err := chat.IndexConversationMessages(withToken(ctx, writerToken), indexRequest)
	if err != nil {
		t.Fatalf("replay index: %v", err)
	}
	if len(replayed.GetStates()) != 1 || replayed.GetStates()[0].GetRef().GetMessageId() != "m-1" {
		t.Fatalf("replayed index = %+v", replayed.GetStates())
	}
	if replayed.GetStates()[0].GetRetracted() {
		t.Fatalf("replay returned the current retracted state instead of the recorded first response: %+v", replayed.GetStates()[0])
	}
}

func searchContainerChat(
	t *testing.T,
	client mixinsearchchatv1.ChatIndexServiceClient,
	ctx context.Context,
	token string,
	query string,
	scopeID string,
) []*mixinsearchchatv1.ChatSearchHit {
	t.Helper()
	response, err := client.SearchChatMessages(withToken(ctx, token), &mixinsearchchatv1.SearchChatMessagesRequest{
		Query: query, AllowedScopeIds: []string{scopeID}, TopK: 5,
	})
	if err != nil {
		t.Fatalf("search chat messages: %v", err)
	}
	return response.GetHits()
}

func mustIssue(
	t *testing.T,
	issuer *security.Issuer,
	subject string,
	role string,
	userID string,
	spaceIDs []string,
	documentIDs []string,
) string {
	t.Helper()
	token, err := issuer.Issue(subject, role, userID, spaceIDs, documentIDs)
	if err != nil {
		t.Fatalf("issue %s token: %v", role, err)
	}
	return token
}

// trimBoundaryKey removes the trailing newline an editor or bootstrap script may
// have left in the mounted key file; the deployed service reads the same file.
func trimBoundaryKey(key []byte) []byte {
	for len(key) > 0 && (key[len(key)-1] == '\n' || key[len(key)-1] == '\r') {
		key = key[:len(key)-1]
	}
	return key
}
