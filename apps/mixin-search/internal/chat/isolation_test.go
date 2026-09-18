package chat

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"mixin-search/internal/rag"
)

// countingDocumentStore wraps the document control store and counts how often
// the chat corpus's activity makes the document control plane do anything. The
// isolation requirement is that the answer is never.
type countingDocumentStore struct {
	inner       rag.ControlStore
	loads       atomic.Int64
	generations atomic.Int64
	saves       atomic.Int64
}

func (s *countingDocumentStore) Load(ctx context.Context) (rag.ControlState, error) {
	s.loads.Add(1)
	return s.inner.Load(ctx)
}

func (s *countingDocumentStore) Generation(ctx context.Context) (uint64, error) {
	s.generations.Add(1)
	return s.inner.Generation(ctx)
}

func (s *countingDocumentStore) Save(ctx context.Context, expected uint64, state rag.ControlState) (uint64, error) {
	s.saves.Add(1)
	return s.inner.Save(ctx, expected, state)
}

func (s *countingDocumentStore) StorageDomain() string { return s.inner.StorageDomain() }

type documentHarness struct {
	service *rag.DocumentIndexService
	store   *countingDocumentStore
}

func newDocumentHarness(t *testing.T) *documentHarness {
	t.Helper()
	core, err := rag.NewServiceWithStore(context.Background(), rag.NewMemoryStore())
	if err != nil {
		t.Fatalf("new document core service: %v", err)
	}
	t.Cleanup(func() { _ = core.Close() })
	store := &countingDocumentStore{inner: rag.NewMemoryControlStore()}
	service, err := rag.NewDocumentIndexServiceWithControlStore(context.Background(), core, store)
	if err != nil {
		t.Fatalf("new document index service: %v", err)
	}
	return &documentHarness{service: service, store: store}
}

func (h *documentHarness) indexAndActivate(t *testing.T, documentID, content string) {
	t.Helper()
	if _, err := h.service.IndexDocumentVersion(context.Background(), rag.IndexDocumentVersionRequest{
		OperationID: "doc-index-" + documentID, DocumentID: documentID, VersionID: "v1",
		OwnerSpaceID: "owner-space", Filename: documentID + ".md", Content: []byte(content),
		LifecycleRevision: 1,
	}); err != nil {
		t.Fatalf("index document: %v", err)
	}
	if _, err := h.service.ActivateDocumentVersion(context.Background(), rag.ActivateDocumentVersionRequest{
		OperationID: "doc-activate-" + documentID, DocumentID: documentID, VersionID: "v1",
		ActivationRevision: 1, LifecycleRevision: 1,
	}); err != nil {
		t.Fatalf("activate document: %v", err)
	}
	if _, err := h.service.UpdateDocumentAccess(context.Background(), rag.UpdateDocumentAccessRequest{
		OperationID: "doc-access-" + documentID, DocumentID: documentID, AccessRevision: 1,
		LifecycleRevision: 1, AuthenticatedPublic: true,
	}); err != nil {
		t.Fatalf("update document access: %v", err)
	}
}

func documentPayloadSize(t *testing.T, store rag.ControlStore) int {
	t.Helper()
	state, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("load document control state: %v", err)
	}
	payload, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal document control state: %v", err)
	}
	return len(payload)
}

// TestChatActivityDoesNotTouchTheDocumentControlPlane is ADR-014's first
// acceptance condition in its strongest form: chat writes, archive decisions,
// access snapshots and retractions must leave the document control plane
// completely untouched — not merely unchanged in value, but never even loaded.
func TestChatActivityDoesNotTouchTheDocumentControlPlane(t *testing.T) {
	documents := newDocumentHarness(t)
	chatHarness := newHarness(t)

	// Measure the payload first: measuring it loads the control plane, and that
	// load must not be attributed to chat activity below.
	payloadBefore := documentPayloadSize(t, documents.store)
	loadsBefore := documents.store.loads.Load()
	generationsBefore := documents.store.generations.Load()
	savesBefore := documents.store.saves.Load()

	for index := 0; index < 25; index++ {
		conversationID := "group-" + string(rune('a'+index%26))
		operationID := "chat-op-" + string(rune('a'+index%26))
		chatHarness.index(t, operationID, conversationID, "scope-"+conversationID, 1, "isolationneedle")
		chatHarness.archive(t, operationID+"-archive", conversationID, 1, 1)
		if _, err := chatHarness.service.UpdateConversationAccess(context.Background(), UpdateConversationAccessRequest{
			OperationID: operationID + "-access", ConversationID: conversationID,
			AccessRevision: 1, LifecycleRevision: 1, GrantedScopeIDs: []string{"scope-shared"},
		}); err != nil {
			t.Fatalf("chat access update: %v", err)
		}
		if _, err := chatHarness.service.RetractMessage(context.Background(), RetractMessageRequest{
			OperationID: operationID + "-retract", ConversationID: conversationID, MessageID: "isolationneedle",
			RetractRevision: 1, LifecycleRevision: 1,
		}); err != nil {
			t.Fatalf("chat retraction: %v", err)
		}
	}

	if got := documents.store.loads.Load() - loadsBefore; got != 0 {
		t.Fatalf("chat activity caused %d document control-plane loads", got)
	}
	if got := documents.store.generations.Load() - generationsBefore; got != 0 {
		t.Fatalf("chat activity caused %d document generation probes", got)
	}
	if got := documents.store.saves.Load() - savesBefore; got != 0 {
		t.Fatalf("chat activity caused %d document control-plane saves", got)
	}
	if after := documentPayloadSize(t, documents.store); after != payloadBefore {
		t.Fatalf("document control payload grew from %d to %d bytes because of chat activity", payloadBefore, after)
	}
}

// TestDocumentActivityDoesNotAdvanceTheChatGeneration is the same requirement in
// the other direction: the corpora have independent generations.
func TestDocumentActivityDoesNotAdvanceTheChatGeneration(t *testing.T) {
	documents := newDocumentHarness(t)
	chatHarness := newHarness(t)
	chatHarness.index(t, "chat-op", "group-42", "scope-group-42", 1, "keepneedle")
	chatGeneration := chatHarness.service.state.Generation()
	chatMessages := len(chatHarness.service.state.Load().messages)

	documents.indexAndActivate(t, "doc-1", "documentneedle")

	if got := chatHarness.service.state.Generation(); got != chatGeneration {
		t.Fatalf("chat generation moved from %d to %d because of document activity", chatGeneration, got)
	}
	if got := len(chatHarness.service.state.Load().messages); got != chatMessages {
		t.Fatalf("chat message count changed from %d to %d", chatMessages, got)
	}
}

// TestChatScaleDoesNotGrowTheDocumentSnapshot checks the sizing consequence: a
// large chat corpus must not inflate the document control snapshot, which is the
// cost that would appear if both corpora shared one state object.
func TestChatScaleDoesNotGrowTheDocumentSnapshot(t *testing.T) {
	documents := newDocumentHarness(t)
	chatHarness := newHarness(t)

	payloadBefore := documentPayloadSize(t, documents.store)
	for index := 0; index < 200; index++ {
		chatHarness.index(t, "scale-op-"+string(rune('a'+index%26)),
			"group-"+string(rune('a'+index%26)), "scope-group", 1, "scaleneedle-"+string(rune('a'+index%26)))
	}
	if got := len(chatHarness.service.state.Load().messages); got == 0 {
		t.Fatal("chat corpus stayed empty, so the assertion below is vacuous")
	}
	if after := documentPayloadSize(t, documents.store); after != payloadBefore {
		t.Fatalf("document control payload grew from %d to %d bytes with chat scale", payloadBefore, after)
	}
}

// TestChatProjectionFailureDoesNotAffectDocumentRetrieval proves the failure
// domains are separate: a chat collection that cannot be written must not stop
// document search.
func TestChatProjectionFailureDoesNotAffectDocumentRetrieval(t *testing.T) {
	documents := newDocumentHarness(t)
	documents.indexAndActivate(t, "doc-1", "documentneedle")

	chatHarness := newHarness(t)
	chatHarness.index(t, "chat-op", "group-42", "scope-group-42", 1, "chatneedle")
	chatHarness.archive(t, "chat-op-archive", "group-42", 1, 1)
	chatHarness.indexer.mu.Lock()
	chatHarness.indexer.syncErr = errors.New("chat collection unavailable")
	chatHarness.indexer.mu.Unlock()

	result, err := documents.service.SearchDocuments(context.Background(), rag.SearchDocumentsRequest{
		Query: "documentneedle", TopK: 1,
	})
	if err != nil {
		t.Fatalf("document search failed while the chat projection was broken: %v", err)
	}
	if len(result.Hits) != 1 {
		t.Fatalf("document hits = %d, want 1", len(result.Hits))
	}
}

// blockingProjectionStore holds the chat projection open, so a test can prove
// that document work does not queue behind it.
type blockingProjectionStore struct {
	entered chan struct{}
	release chan struct{}
	once    chan struct{}
}

func newBlockingProjectionStore() *blockingProjectionStore {
	return &blockingProjectionStore{
		entered: make(chan struct{}),
		release: make(chan struct{}),
		once:    make(chan struct{}, 1),
	}
}

func (s *blockingProjectionStore) SyncChatControls(ctx context.Context, _ []VectorControl) error {
	select {
	case s.once <- struct{}{}:
		close(s.entered)
	default:
	}
	select {
	case <-s.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

// TestDocumentRevocationDoesNotWaitForTheChatProjection is ADR-014's third
// acceptance condition: revoking document access must not depend on the chat
// corpus converging its projection.
func TestDocumentRevocationDoesNotWaitForTheChatProjection(t *testing.T) {
	documents := newDocumentHarness(t)
	documents.indexAndActivate(t, "doc-1", "revocationneedle")

	projection := newBlockingProjectionStore()
	indexer := newFakeIndexer()
	chatService, err := NewIndexService(context.Background(), IndexServiceConfig{
		ControlStore: NewMemoryControlStore(),
		Indexer:      indexer,
		Projection:   projection,
		Searcher:     &fakeSearcher{indexer: indexer},
	})
	if err != nil {
		t.Fatalf("new chat index service: %v", err)
	}
	if _, err := chatService.IndexMessages(context.Background(), IndexMessagesRequest{
		OperationID: "chat-op", ConversationID: "group-42", OwnerScopeID: "scope-group-42",
		LifecycleRevision: 1,
		Messages:          []MessageInput{{MessageID: "m-1", SenderID: "sender", SentAtUnixMs: 1, Content: "blocked"}},
	}); err != nil {
		t.Fatalf("chat index: %v", err)
	}

	// Start a chat convergence that blocks inside the vector store.
	go func() {
		_, _ = chatService.SearchMessages(context.Background(), SearchMessagesRequest{
			Query: "blocked", AllowedScopeIDs: []string{"scope-group-42"}, TopK: 1,
		})
	}()
	select {
	case <-projection.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the chat projection never started, so the test cannot prove anything")
	}

	done := make(chan error, 1)
	go func() {
		_, err := documents.service.UpdateDocumentAccess(context.Background(), rag.UpdateDocumentAccessRequest{
			OperationID: "revoke", DocumentID: "doc-1", AccessRevision: 2, LifecycleRevision: 1,
		})
		if err == nil {
			_, err = documents.service.SearchDocuments(context.Background(), rag.SearchDocumentsRequest{
				Query: "revocationneedle", TopK: 1,
			})
		}
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("document revocation while the chat projection was blocked: %v", err)
		}
	case <-time.After(5 * time.Second):
		close(projection.release)
		t.Fatal("document revocation waited for the chat projection")
	}
	close(projection.release)
}
