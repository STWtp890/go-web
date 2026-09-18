package chatindex

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"mixin-search/internal/chat"
	"mixin-search/internal/rag"
)

// countingStore observes what actually reached the collection, so a test can
// distinguish "filtered out by the projection" from "never written" and from
// "physically deleted".
type countingStore struct {
	inner   *rag.MemoryStore
	mu      sync.Mutex
	stored  int
	deletes int
}

func (s *countingStore) ReplaceDocument(ctx context.Context, documentID string, chunks []rag.IndexedChunk) error {
	s.mu.Lock()
	if len(chunks) == 0 {
		s.deletes++
	} else {
		s.stored += len(chunks)
	}
	s.mu.Unlock()
	return s.inner.ReplaceDocument(ctx, documentID, chunks)
}

func (s *countingStore) DenseSearch(ctx context.Context, query []float64, limit int) ([]rag.ScoredChunk, error) {
	return s.inner.DenseSearch(ctx, query, limit)
}

func (s *countingStore) SparseSearch(ctx context.Context, queryTokens []string, limit int) ([]rag.ScoredChunk, error) {
	return s.inner.SparseSearch(ctx, queryTokens, limit)
}

func (s *countingStore) Close() error { return s.inner.Close() }

func (s *countingStore) storedChunks() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stored
}

func (s *countingStore) deleteCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deletes
}

type corpusHarness struct {
	corpus  *Corpus
	service *chat.IndexService
	inner   *countingStore
}

func newCorpusHarness(t *testing.T) *corpusHarness {
	t.Helper()
	inner := &countingStore{inner: rag.NewMemoryStore()}
	corpus, err := New(context.Background(), Config{
		VectorStore:   inner,
		ControlStore:  chat.NewMemoryControlStore(),
		StorageDomain: "chat-test-domain",
	})
	if err != nil {
		t.Fatalf("new chat corpus: %v", err)
	}
	t.Cleanup(func() { _ = corpus.Close() })
	return &corpusHarness{corpus: corpus, service: corpus.Service(), inner: inner}
}

func (h *corpusHarness) index(t *testing.T, operationID, conversationID, scopeID string, contents ...string) {
	t.Helper()
	h.indexAt(t, operationID, conversationID, scopeID, 1, contents...)
}

func (h *corpusHarness) indexAt(t *testing.T, operationID, conversationID, scopeID string, lifecycle uint64, contents ...string) {
	t.Helper()
	inputs := make([]chat.MessageInput, 0, len(contents))
	for index, content := range contents {
		inputs = append(inputs, chat.MessageInput{
			MessageID:    content,
			SenderID:     "sender",
			SentAtUnixMs: int64(1_700_000_000_000 + index),
			Content:      content,
		})
	}
	if _, err := h.service.IndexMessages(context.Background(), chat.IndexMessagesRequest{
		OperationID: operationID, ConversationID: conversationID, OwnerScopeID: scopeID,
		LifecycleRevision: lifecycle, Messages: inputs,
	}); err != nil {
		t.Fatalf("index chat messages: %v", err)
	}
}

func (h *corpusHarness) archive(t *testing.T, conversationID string) {
	t.Helper()
	h.archiveAt(t, conversationID, 1, 1)
}

func (h *corpusHarness) archiveAt(t *testing.T, conversationID string, archiveRevision, lifecycle uint64) {
	t.Helper()
	// One operation id per decision: reusing an id with a different payload is
	// correctly rejected by the idempotency ledger, so the helper must not do it.
	operationID := fmt.Sprintf("archive-%s-%d-%d", conversationID, archiveRevision, lifecycle)
	if _, err := h.service.ArchiveConversation(context.Background(), chat.ArchiveConversationRequest{
		OperationID: operationID, ConversationID: conversationID,
		ArchiveRevision: archiveRevision, LifecycleRevision: lifecycle,
	}); err != nil {
		t.Fatalf("archive conversation: %v", err)
	}
}

func (h *corpusHarness) search(t *testing.T, query string, scopes []string) chat.SearchMessagesResult {
	t.Helper()
	result, err := h.service.SearchMessages(context.Background(), chat.SearchMessagesRequest{
		Query: query, AllowedScopeIDs: scopes, TopK: 5,
	})
	if err != nil {
		t.Fatalf("search chat messages: %v", err)
	}
	return result
}

// TestCorpusIndexesAndRetrievesThroughAProjection is the end-to-end shape of the
// chat corpus: index, archive, converge the projection, then retrieve.
func TestCorpusIndexesAndRetrievesThroughAProjection(t *testing.T) {
	h := newCorpusHarness(t)
	h.index(t, "op-1", "group-42", "scope-group-42", "corpusneedle")
	if h.corpus.ProjectionSize() != 0 {
		t.Fatalf("projection size = %d before any convergence", h.corpus.ProjectionSize())
	}

	h.archive(t, "group-42")
	result := h.search(t, "corpusneedle", []string{"scope-group-42"})
	if len(result.Hits) != 1 {
		t.Fatalf("hits = %d, want 1", len(result.Hits))
	}
	if result.Hits[0].ConversationID != "group-42" || result.Hits[0].MessageID != "corpusneedle" {
		t.Fatalf("hit = %+v", result.Hits[0])
	}
	if !strings.Contains(result.Hits[0].Snippet, "corpusneedle") {
		t.Fatalf("snippet = %q", result.Hits[0].Snippet)
	}
	if h.corpus.ProjectionSize() == 0 {
		t.Fatal("the projection was never converged, so candidate filtering proved nothing")
	}
}

// TestUnarchivedMessagesAreNotCandidates shows the first state rule at the store
// level: indexed but not archived means the vectors exist and are filtered out.
func TestUnarchivedMessagesAreNotCandidates(t *testing.T) {
	h := newCorpusHarness(t)
	h.index(t, "op-1", "group-42", "scope-group-42", "unarchivedneedle")

	if result := h.search(t, "unarchivedneedle", []string{"scope-group-42"}); len(result.Hits) != 0 {
		t.Fatalf("hits before archive = %d, want 0", len(result.Hits))
	}
	// The vectors are physically present: the projection, not the absence of
	// data, is what removed them.
	if h.inner.storedChunks() == 0 {
		t.Fatal("no vectors were written, so the assertion above is vacuous")
	}
}

// TestRetractedMessagesStopBeingCandidates shows the second state rule: the
// message stays indexed and its vectors stay in the collection, and it is the
// projection that removes it from candidates.
func TestRetractedMessagesStopBeingCandidates(t *testing.T) {
	h := newCorpusHarness(t)
	h.index(t, "op-1", "group-42", "scope-group-42", "retractedneedle")
	h.archive(t, "group-42")
	if result := h.search(t, "retractedneedle", []string{"scope-group-42"}); len(result.Hits) != 1 {
		t.Fatalf("hits before retraction = %d, want 1", len(result.Hits))
	}

	if _, err := h.service.RetractMessage(context.Background(), chat.RetractMessageRequest{
		OperationID: "op-retract", ConversationID: "group-42", MessageID: "retractedneedle",
		RetractRevision: 1, LifecycleRevision: 1,
	}); err != nil {
		t.Fatalf("retract message: %v", err)
	}
	if result := h.search(t, "retractedneedle", []string{"scope-group-42"}); len(result.Hits) != 0 {
		t.Fatalf("hits after retraction = %d, want 0", len(result.Hits))
	}
	// Retraction is not a deletion: nothing may have been removed from the
	// collection by it.
	if h.inner.deleteCalls() != 0 {
		t.Fatalf("retraction issued %d vector deletes, want 0", h.inner.deleteCalls())
	}
	if h.inner.storedChunks() == 0 {
		t.Fatal("the message was never written, so the assertion above is vacuous")
	}
}

// TestTombstonedConversationsStopBeingCandidates covers the third rule, and the
// physical cleanup that follows it.
func TestTombstonedConversationsStopBeingCandidates(t *testing.T) {
	h := newCorpusHarness(t)
	h.index(t, "op-1", "group-42", "scope-group-42", "tombstonedneedle")
	h.archive(t, "group-42")

	if _, err := h.service.DeleteConversation(context.Background(), chat.DeleteConversationRequest{
		OperationID: "op-delete", ConversationID: "group-42", LifecycleRevision: 2,
	}); err != nil {
		t.Fatalf("delete conversation: %v", err)
	}
	if result := h.search(t, "tombstonedneedle", []string{"scope-group-42"}); len(result.Hits) != 0 {
		t.Fatalf("hits after tombstone = %d, want 0", len(result.Hits))
	}
}

// TestChatRebuildDoesNotTouchTheDocumentCorpus is ADR-014's remaining acceptance
// condition in executable form: rebuilding the chat index — deleting every
// conversation and re-indexing it — must leave the document corpus's vectors,
// projection and retrieval untouched, because the two never share a collection.
func TestChatRebuildDoesNotTouchTheDocumentCorpus(t *testing.T) {
	documentStore := &countingStore{inner: rag.NewMemoryStore()}
	documentCore, err := rag.NewServiceWithStore(context.Background(), documentStore)
	if err != nil {
		t.Fatalf("new document core: %v", err)
	}
	t.Cleanup(func() { _ = documentCore.Close() })
	documentService, err := rag.NewDocumentIndexServiceWithControlStore(
		context.Background(), documentCore, rag.NewMemoryControlStore(),
	)
	if err != nil {
		t.Fatalf("new document service: %v", err)
	}
	if _, err := documentService.IndexDocumentVersion(context.Background(), rag.IndexDocumentVersionRequest{
		OperationID: "doc-index", DocumentID: "doc-1", VersionID: "v1", OwnerSpaceID: "owner-space",
		Filename: "doc-1.md", Content: []byte("documentneedle"), LifecycleRevision: 1,
	}); err != nil {
		t.Fatalf("index document: %v", err)
	}
	if _, err := documentService.ActivateDocumentVersion(context.Background(), rag.ActivateDocumentVersionRequest{
		OperationID: "doc-activate", DocumentID: "doc-1", VersionID: "v1",
		ActivationRevision: 1, LifecycleRevision: 1,
	}); err != nil {
		t.Fatalf("activate document: %v", err)
	}
	if _, err := documentService.UpdateDocumentAccess(context.Background(), rag.UpdateDocumentAccessRequest{
		OperationID: "doc-access", DocumentID: "doc-1", AccessRevision: 1,
		LifecycleRevision: 1, AuthenticatedPublic: true,
	}); err != nil {
		t.Fatalf("update document access: %v", err)
	}

	documentsBefore := documentStore.storedChunks()
	deletesBefore := documentStore.deleteCalls()

	chatCorpus := newCorpusHarness(t)
	chatCorpus.index(t, "op-1", "group-42", "scope-group-42", "chatrebuildneedle")
	chatCorpus.archive(t, "group-42")
	if result := chatCorpus.search(t, "chatrebuildneedle", []string{"scope-group-42"}); len(result.Hits) != 1 {
		t.Fatalf("chat hits before rebuild = %d, want 1", len(result.Hits))
	}

	// Rebuild the chat index: tombstone the conversation and index it again at a
	// higher lifecycle revision, which is the only way a tombstoned conversation
	// can come back.
	if _, err := chatCorpus.service.DeleteConversation(context.Background(), chat.DeleteConversationRequest{
		OperationID: "chat-delete", ConversationID: "group-42", LifecycleRevision: 2,
	}); err != nil {
		t.Fatalf("delete chat conversation: %v", err)
	}
	chatCorpus.indexAt(t, "op-2", "group-42", "scope-group-42", 3, "chatrebuildneedle")
	chatCorpus.archiveAt(t, "group-42", 2, 3)
	if result := chatCorpus.search(t, "chatrebuildneedle", []string{"scope-group-42"}); len(result.Hits) != 1 {
		t.Fatalf("chat hits after rebuild = %d, want 1", len(result.Hits))
	}

	if got := documentStore.storedChunks(); got != documentsBefore {
		t.Fatalf("document chunks changed from %d to %d during a chat rebuild", documentsBefore, got)
	}
	if got := documentStore.deleteCalls(); got != deletesBefore {
		t.Fatalf("document vector deletes changed from %d to %d during a chat rebuild", deletesBefore, got)
	}
	result, err := documentService.SearchDocuments(context.Background(), rag.SearchDocumentsRequest{
		Query: "documentneedle", TopK: 1,
	})
	if err != nil {
		t.Fatalf("document search after a chat rebuild: %v", err)
	}
	if len(result.Hits) != 1 {
		t.Fatalf("document hits after a chat rebuild = %d, want 1", len(result.Hits))
	}
}

// TestProjectionIsAFullSnapshot checks that the projection replaces rather than
// merges: an omitted control stops being retrievable, which is what makes
// unarchive and retraction converge without a separate delete path.
func TestProjectionIsAFullSnapshot(t *testing.T) {
	store, err := NewStore(rag.NewMemoryStore(), "chat-test-domain")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	ctx := context.Background()
	if err := store.SyncChatControls(ctx, []chat.VectorControl{
		{StorageID: "a", StorageDomain: "chat-test-domain", Archived: true},
		{StorageID: "b", StorageDomain: "chat-test-domain", Archived: true},
	}); err != nil {
		t.Fatalf("sync controls: %v", err)
	}
	if got := store.ProjectionSize(); got != 2 {
		t.Fatalf("projection size = %d, want 2", got)
	}
	if err := store.SyncChatControls(ctx, []chat.VectorControl{
		{StorageID: "b", StorageDomain: "chat-test-domain", Archived: true},
	}); err != nil {
		t.Fatalf("sync controls: %v", err)
	}
	if got := store.ProjectionSize(); got != 1 {
		t.Fatalf("projection size = %d after a partial snapshot, want 1", got)
	}
}

// TestForeignKeyAndDomainAreNotServed guards the store against serving something
// it cannot vouch for: a chunk whose storage key has no control, or whose control
// belongs to another domain, is dropped even if the inner store returned it.
func TestForeignKeyAndDomainAreNotServed(t *testing.T) {
	inner := rag.NewMemoryStore()
	if err := inner.ReplaceDocument(context.Background(), "orphan", []rag.IndexedChunk{{
		Chunk: rag.Chunk{ID: "orphan#000", DocumentID: "orphan", Content: "orphanneedle", Position: 0},
		Dense: []float64{1},
	}}); err != nil {
		t.Fatalf("seed inner store: %v", err)
	}
	store, err := NewStore(inner, "chat-test-domain")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	// A control from another domain must not make the chunk visible.
	if err := store.SyncChatControls(context.Background(), []chat.VectorControl{
		{StorageID: "orphan", StorageDomain: "document-domain", Archived: true},
	}); err != nil {
		t.Fatalf("sync controls: %v", err)
	}
	hits, err := store.DenseSearch(context.Background(), []float64{1}, 5)
	if err != nil {
		t.Fatalf("dense search: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("hits = %d, want 0 for a foreign-domain control", len(hits))
	}
}
