package chat

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

// fakeIndexer records the messages this corpus wrote to its collection and keeps
// the text so the fake searcher can match on it. It is the only path from the
// control plane to a vector store, so counting its calls is how the tests show
// what the control plane actually did.
type fakeIndexer struct {
	mu        sync.Mutex
	messages  map[string]indexedMessage
	deleted   []string
	controls  []VectorControl
	syncCalls int
	indexErr  error
	syncErr   error
	deleteErr error
}

type indexedMessage struct {
	conversationID string
	messageID      string
	content        string
	chunkCount     int
}

func newFakeIndexer() *fakeIndexer {
	return &fakeIndexer{messages: make(map[string]indexedMessage)}
}

func (f *fakeIndexer) IndexMessage(_ context.Context, storageID string, message MessageInput) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.indexErr != nil {
		return 0, f.indexErr
	}
	const chunks = 1
	f.messages[storageID] = indexedMessage{
		conversationID: message.MessageID, messageID: message.MessageID,
		content: message.Content, chunkCount: chunks,
	}
	// The conversation id is not in MessageInput, so record it from the storage
	// key; the tests use it only to build candidates.
	return chunks, nil
}

// IndexMessageWithConversation is a test helper: the production indexer receives
// the conversation through the storage id it was derived from.
func (f *fakeIndexer) record(storageID, conversationID, messageID, content string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messages[storageID] = indexedMessage{conversationID: conversationID, messageID: messageID, content: content, chunkCount: 1}
}

func (f *fakeIndexer) DeleteMessage(_ context.Context, storageID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleted = append(f.deleted, storageID)
	delete(f.messages, storageID)
	return nil
}

func (f *fakeIndexer) SyncChatControls(_ context.Context, controls []VectorControl) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.syncCalls++
	if f.syncErr != nil {
		return f.syncErr
	}
	f.controls = append([]VectorControl(nil), controls...)
	return nil
}

func (f *fakeIndexer) indexedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.messages)
}

func (f *fakeIndexer) controlCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.controls)
}

func (f *fakeIndexer) syncCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.syncCalls
}

// fakeSearcher returns every indexed message whose text contains the query,
// which is enough to exercise the control plane's retrievability rules.
type fakeSearcher struct {
	indexer *fakeIndexer
	err     error
	calls   int
	limit   int
}

func (f *fakeSearcher) SearchMessages(_ context.Context, query string, limit int) ([]ScoredMessageChunk, error) {
	f.calls++
	f.limit = limit
	if f.err != nil {
		return nil, f.err
	}
	f.indexer.mu.Lock()
	defer f.indexer.mu.Unlock()
	candidates := make([]ScoredMessageChunk, 0, limit)
	for storageID, message := range f.indexer.messages {
		if !strings.Contains(message.content, query) {
			continue
		}
		candidates = append(candidates, ScoredMessageChunk{
			ConversationID: message.conversationID,
			MessageID:      message.messageID,
			StorageID:      storageID,
			Position:       0,
			Snippet:        message.content,
			Score:          1,
		})
		if len(candidates) == limit {
			break
		}
	}
	return candidates, nil
}

type harness struct {
	service  *IndexService
	indexer  *fakeIndexer
	searcher *fakeSearcher
	store    *MemoryControlStore
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	indexer := newFakeIndexer()
	searcher := &fakeSearcher{indexer: indexer}
	store := NewMemoryControlStore()
	service, err := NewIndexService(context.Background(), IndexServiceConfig{
		ControlStore: store,
		Indexer:      indexer,
		Projection:   indexer,
		Searcher:     searcher,
	})
	if err != nil {
		t.Fatalf("new chat index service: %v", err)
	}
	return &harness{service: service, indexer: indexer, searcher: searcher, store: store}
}

func (h *harness) index(t *testing.T, operationID, conversationID, scopeID string, lifecycle uint64, contents ...string) []MessageState {
	t.Helper()
	inputs := make([]MessageInput, 0, len(contents))
	for index, content := range contents {
		inputs = append(inputs, MessageInput{
			MessageID:    content,
			SenderID:     "sender",
			SentAtUnixMs: int64(1_700_000_000_000 + index),
			Content:      content,
		})
	}
	states, err := h.service.IndexMessages(context.Background(), IndexMessagesRequest{
		OperationID: operationID, ConversationID: conversationID, OwnerScopeID: scopeID,
		LifecycleRevision: lifecycle, Messages: inputs,
	})
	if err != nil {
		t.Fatalf("index messages: %v", err)
	}
	// The production indexer receives the conversation id through the storage id
	// the control plane derived; the fake needs it to build candidates.
	for _, state := range states {
		h.indexer.record(storageIDFor(h.service, conversationID, state.MessageID, operationID),
			conversationID, state.MessageID, state.MessageID)
	}
	return states
}

func storageIDFor(service *IndexService, conversationID, messageID, operationID string) string {
	return storageID(service.StorageDomain(), conversationID, messageID, operationID)
}

func (h *harness) archive(t *testing.T, operationID, conversationID string, archiveRevision, lifecycle uint64) {
	t.Helper()
	if _, err := h.service.ArchiveConversation(context.Background(), ArchiveConversationRequest{
		OperationID: operationID, ConversationID: conversationID,
		ArchiveRevision: archiveRevision, LifecycleRevision: lifecycle,
	}); err != nil {
		t.Fatalf("archive conversation: %v", err)
	}
}

func (h *harness) search(t *testing.T, query string, scopes, conversations []string) SearchMessagesResult {
	t.Helper()
	result, err := h.service.SearchMessages(context.Background(), SearchMessagesRequest{
		Query: query, AllowedScopeIDs: scopes, AllowedConversationIDs: conversations, TopK: 5,
	})
	if err != nil {
		t.Fatalf("search messages: %v", err)
	}
	return result
}

// TestIndexingAloneIsNotSearchable is the heart of the corpus's semantics:
// accepting message text never makes it retrievable. Only an explicit archive
// decision does, which is what keeps "stored", "indexed" and "archived" apart.
func TestIndexingAloneIsNotSearchable(t *testing.T) {
	h := newHarness(t)
	h.index(t, "op-1", "group-42", "scope-group-42", 1, "deployneedle")

	if result := h.search(t, "deployneedle", []string{"scope-group-42"}, nil); len(result.Hits) != 0 {
		t.Fatalf("hits before archive = %d, want 0", len(result.Hits))
	}

	h.archive(t, "op-2", "group-42", 1, 1)
	result := h.search(t, "deployneedle", []string{"scope-group-42"}, nil)
	if len(result.Hits) != 1 {
		t.Fatalf("hits after archive = %d, want 1", len(result.Hits))
	}
	if result.Hits[0].ConversationID != "group-42" || result.Hits[0].MessageID != "deployneedle" {
		t.Fatalf("hit = %+v", result.Hits[0])
	}
}

// TestRetractionRemovesFromRetrievalButKeepsTheMessage pins "retracted is not
// deleted": the message stays indexed and counted, and simply stops matching.
func TestRetractionRemovesFromRetrievalButKeepsTheMessage(t *testing.T) {
	h := newHarness(t)
	h.index(t, "op-1", "group-42", "scope-group-42", 1, "retractneedle")
	h.archive(t, "op-2", "group-42", 1, 1)
	if result := h.search(t, "retractneedle", []string{"scope-group-42"}, nil); len(result.Hits) != 1 {
		t.Fatalf("hits before retraction = %d, want 1", len(result.Hits))
	}

	if _, err := h.service.RetractMessage(context.Background(), RetractMessageRequest{
		OperationID: "op-3", ConversationID: "group-42", MessageID: "retractneedle",
		RetractRevision: 1, LifecycleRevision: 1,
	}); err != nil {
		t.Fatalf("retract message: %v", err)
	}
	if result := h.search(t, "retractneedle", []string{"scope-group-42"}, nil); len(result.Hits) != 0 {
		t.Fatalf("hits after retraction = %d, want 0", len(result.Hits))
	}

	state, err := h.service.GetConversationIndexState(context.Background(), GetConversationIndexStateRequest{ConversationID: "group-42"})
	if err != nil {
		t.Fatalf("conversation state: %v", err)
	}
	if state.IndexedMessageCount != 1 || state.RetractedMessageCount != 1 {
		t.Fatalf("state = %+v, want the message still indexed and counted as retracted", state)
	}
	if state.Status != StatusArchived {
		t.Fatalf("status = %q, want the conversation still archived", state.Status)
	}
}

// TestFourRevisionsAdvanceIndependently guards the decision that archive, access,
// lifecycle and retraction are separate high-water marks.
func TestFourRevisionsAdvanceIndependently(t *testing.T) {
	h := newHarness(t)
	h.index(t, "op-1", "group-42", "scope-group-42", 3, "revisionneedle")
	h.archive(t, "op-2", "group-42", 7, 3)
	if _, err := h.service.UpdateConversationAccess(context.Background(), UpdateConversationAccessRequest{
		OperationID: "op-3", ConversationID: "group-42", AccessRevision: 5, LifecycleRevision: 3,
		GrantedScopeIDs: []string{"scope-extra"},
	}); err != nil {
		t.Fatalf("update access: %v", err)
	}
	if _, err := h.service.RetractMessage(context.Background(), RetractMessageRequest{
		OperationID: "op-4", ConversationID: "group-42", MessageID: "revisionneedle",
		RetractRevision: 2, LifecycleRevision: 3,
	}); err != nil {
		t.Fatalf("retract: %v", err)
	}

	state, err := h.service.GetConversationIndexState(context.Background(), GetConversationIndexStateRequest{ConversationID: "group-42"})
	if err != nil {
		t.Fatalf("conversation state: %v", err)
	}
	if state.ArchiveRevision != 7 || state.AccessRevision != 5 || state.LifecycleRevision != 3 {
		t.Fatalf("revisions = archive %d access %d lifecycle %d, want 7 / 5 / 3", state.ArchiveRevision, state.AccessRevision, state.LifecycleRevision)
	}
}

// TestDeleteTombstonesAndRejectsLateEvents pins the lifecycle fence: a deleted
// conversation can only come back at a higher lifecycle revision, and the old
// decision stays replayable.
func TestDeleteTombstonesAndRejectsLateEvents(t *testing.T) {
	h := newHarness(t)
	h.index(t, "op-1", "group-42", "scope-group-42", 1, "deleteneedle")
	h.archive(t, "op-2", "group-42", 1, 1)

	result, err := h.service.DeleteConversation(context.Background(), DeleteConversationRequest{
		OperationID: "op-3", ConversationID: "group-42", LifecycleRevision: 2,
	})
	if err != nil || !result.Tombstoned {
		t.Fatalf("delete conversation = %+v err=%v", result, err)
	}
	if hits := h.search(t, "deleteneedle", []string{"scope-group-42"}, nil); len(hits.Hits) != 0 {
		t.Fatalf("hits after tombstone = %d, want 0", len(hits.Hits))
	}

	// A late index event at or below the tombstone revision must not resurrect.
	if _, err := h.service.IndexMessages(context.Background(), IndexMessagesRequest{
		OperationID: "op-late", ConversationID: "group-42", OwnerScopeID: "scope-group-42",
		LifecycleRevision: 2,
		Messages:          []MessageInput{{MessageID: "late", SenderID: "sender", SentAtUnixMs: 1, Content: "lateneedle"}},
	}); !errors.Is(err, ErrStaleLifecycle) {
		t.Fatalf("late index error = %v, want ErrStaleLifecycle", err)
	}

	// Replaying the original delete returns the first response.
	replay, err := h.service.DeleteConversation(context.Background(), DeleteConversationRequest{
		OperationID: "op-3", ConversationID: "group-42", LifecycleRevision: 2,
	})
	if err != nil || replay != result {
		t.Fatalf("delete replay = %+v err=%v, want %+v", replay, err, result)
	}
}

// TestOperationIdReplayAndRebinding shows idempotency: a retry of the same
// payload reuses the first response and writes no second vector, while the same
// operation id with a different payload is a conflict.
func TestOperationIdReplayAndRebinding(t *testing.T) {
	h := newHarness(t)
	h.index(t, "op-1", "group-42", "scope-group-42", 1, "replayneedle")
	after := h.indexer.indexedCount()

	states, err := h.service.IndexMessages(context.Background(), IndexMessagesRequest{
		OperationID: "op-1", ConversationID: "group-42", OwnerScopeID: "scope-group-42",
		LifecycleRevision: 1,
		Messages:          []MessageInput{{MessageID: "replayneedle", SenderID: "sender", SentAtUnixMs: 1_700_000_000_000, Content: "replayneedle"}},
	})
	if err != nil {
		t.Fatalf("replay index: %v", err)
	}
	if len(states) != 1 || states[0].MessageID != "replayneedle" {
		t.Fatalf("replayed states = %+v", states)
	}
	if got := h.indexer.indexedCount(); got != after {
		t.Fatalf("replay wrote %d vectors, want 0", got-after)
	}

	if _, err := h.service.IndexMessages(context.Background(), IndexMessagesRequest{
		OperationID: "op-1", ConversationID: "group-42", OwnerScopeID: "scope-group-42",
		LifecycleRevision: 1,
		Messages:          []MessageInput{{MessageID: "other", SenderID: "sender", SentAtUnixMs: 2, Content: "otherneedle"}},
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("rebinding error = %v, want ErrConflict", err)
	}
}

// TestMessageContentIsImmutable guards the immutable identity of an indexed
// message: the same id with different text is a conflict, not an update.
func TestMessageContentIsImmutable(t *testing.T) {
	h := newHarness(t)
	first, err := h.service.IndexMessages(context.Background(), IndexMessagesRequest{
		OperationID: "op-1", ConversationID: "group-42", OwnerScopeID: "scope-group-42",
		LifecycleRevision: 1,
		Messages:          []MessageInput{{MessageID: "m-1", SenderID: "sender", SentAtUnixMs: 1, Content: "original text"}},
	})
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	if first[0].ContentSHA256 == "" {
		t.Fatal("indexed message is missing its content digest")
	}

	_, err = h.service.IndexMessages(context.Background(), IndexMessagesRequest{
		OperationID: "op-2", ConversationID: "group-42", OwnerScopeID: "scope-group-42",
		LifecycleRevision: 1,
		Messages:          []MessageInput{{MessageID: "m-1", SenderID: "sender", SentAtUnixMs: 1, Content: "edited text"}},
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("edited message error = %v, want ErrConflict", err)
	}
}

// TestChatHasNoPublicCorpus is the authorization difference from documents: two
// empty allow-lists return nothing, and recall is never even attempted.
func TestChatHasNoPublicCorpus(t *testing.T) {
	h := newHarness(t)
	h.index(t, "op-1", "group-42", "scope-group-42", 1, "publicneedle")
	h.archive(t, "op-2", "group-42", 1, 1)
	before := h.searcher.calls

	result := h.search(t, "publicneedle", nil, nil)
	if len(result.Hits) != 0 {
		t.Fatalf("hits without any allow-list = %d, want 0", len(result.Hits))
	}
	if h.searcher.calls != before {
		t.Fatal("an unauthorized search still recalled candidates")
	}

	if hits := h.search(t, "publicneedle", []string{"scope-other"}, nil); len(hits.Hits) != 0 {
		t.Fatalf("hits for an unrelated scope = %d, want 0", len(hits.Hits))
	}
	if hits := h.search(t, "publicneedle", nil, []string{"group-42"}); len(hits.Hits) != 1 {
		t.Fatalf("hits by explicit conversation = %d, want 1", len(hits.Hits))
	}
	if hits := h.search(t, "publicneedle", []string{"scope-shared"}, nil); len(hits.Hits) != 0 {
		t.Fatalf("hits for a scope that was never granted = %d, want 0", len(hits.Hits))
	}
	if _, err := h.service.UpdateConversationAccess(context.Background(), UpdateConversationAccessRequest{
		OperationID: "op-3", ConversationID: "group-42", AccessRevision: 1, LifecycleRevision: 1,
		GrantedScopeIDs: []string{" scope-shared ", "scope-shared", ""},
	}); err != nil {
		t.Fatalf("grant access: %v", err)
	}
	if hits := h.search(t, "publicneedle", []string{"scope-shared"}, nil); len(hits.Hits) != 1 {
		t.Fatalf("hits for a granted scope = %d, want 1", len(hits.Hits))
	}
}

// TestSearchFailsClosedWhenTheProjectionIsUnavailable keeps the safety property:
// a search never serves a snapshot whose derived index could not be converged.
func TestSearchFailsClosedWhenTheProjectionIsUnavailable(t *testing.T) {
	h := newHarness(t)
	h.index(t, "op-1", "group-42", "scope-group-42", 1, "projectionneedle")
	h.archive(t, "op-2", "group-42", 1, 1)

	h.indexer.mu.Lock()
	h.indexer.syncErr = errors.New("chat collection unavailable")
	h.indexer.mu.Unlock()

	if _, err := h.service.SearchMessages(context.Background(), SearchMessagesRequest{
		Query: "projectionneedle", AllowedScopeIDs: []string{"scope-group-42"}, TopK: 1,
	}); !errors.Is(err, ErrProjectionUnavailable) {
		t.Fatalf("search error = %v, want ErrProjectionUnavailable", err)
	}
}

// TestIndexFailureAbandonsTheWriteIntent proves a failed vector write leaves a
// durable cleanup claim rather than an orphan that no intent accounts for.
func TestIndexFailureAbandonsTheWriteIntent(t *testing.T) {
	h := newHarness(t)
	h.indexer.mu.Lock()
	h.indexer.indexErr = errors.New("chat collection unavailable")
	h.indexer.mu.Unlock()

	if _, err := h.service.IndexMessages(context.Background(), IndexMessagesRequest{
		OperationID: "op-1", ConversationID: "group-42", OwnerScopeID: "scope-group-42",
		LifecycleRevision: 1,
		Messages:          []MessageInput{{MessageID: "m-1", SenderID: "sender", SentAtUnixMs: 1, Content: "failneedle"}},
	}); err == nil {
		t.Fatal("indexing succeeded while the collection was unavailable")
	}

	state, err := h.store.Load(context.Background())
	if err != nil {
		t.Fatalf("load control state: %v", err)
	}
	if len(state.PendingWrites) != 0 {
		t.Fatalf("pending writes = %d, want the intent to be abandoned", len(state.PendingWrites))
	}
	if len(state.Messages) != 0 {
		t.Fatalf("messages = %d, want none indexed", len(state.Messages))
	}
}

// TestProjectionCarriesChatControlsAndRetraction checks what the collection is
// told: archived state, retraction and the granted scopes, so candidate
// selection can filter without the control plane in the path.
func TestProjectionCarriesChatControlsAndRetraction(t *testing.T) {
	h := newHarness(t)
	h.index(t, "op-1", "group-42", "scope-group-42", 1, "controlneedle")
	h.archive(t, "op-2", "group-42", 1, 1)
	if _, err := h.service.UpdateConversationAccess(context.Background(), UpdateConversationAccessRequest{
		OperationID: "op-3", ConversationID: "group-42", AccessRevision: 1, LifecycleRevision: 1,
		GrantedScopeIDs: []string{"scope-shared"},
	}); err != nil {
		t.Fatalf("grant access: %v", err)
	}
	h.search(t, "controlneedle", []string{"scope-shared"}, nil)

	h.indexer.mu.Lock()
	controls := h.indexer.controls
	h.indexer.mu.Unlock()
	if len(controls) != 1 {
		t.Fatalf("projection controls = %d, want 1", len(controls))
	}
	control := controls[0]
	if !control.Archived || control.Retracted || control.Tombstoned {
		t.Fatalf("control = %+v, want archived and none of retracted/tombstoned", control)
	}
	if control.OwnerScopeID != "scope-group-42" || len(control.GrantedScopeIDs) != 1 || control.GrantedScopeIDs[0] != "scope-shared" {
		t.Fatalf("control scope = %+v", control)
	}
	if !strings.HasPrefix(control.StorageDomain, "chat-") {
		t.Fatalf("storage domain = %q, want the chat corpus's own domain", control.StorageDomain)
	}
}
