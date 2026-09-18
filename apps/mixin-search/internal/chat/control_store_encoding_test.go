package chat

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

// TestControlStateSurvivesThePersistenceEncoding is the database-free half of
// what the PostgreSQL control store depends on.
//
// The persistent adapter stores the whole control plane as one JSON payload per
// namespace, so every map, revision, ledger entry and tombstone has to survive
// encode/decode and still pass the same validation the store applies on load.
// The memory store the unit tests use never encodes anything, so a field that
// cannot round-trip - an unsupported map key, a field lost to omitempty, a
// decoded state the validator rejects - would only surface against a running
// PostgreSQL, which is exactly what is unavailable here. This test drives the
// same encoding boundary in memory, then proves the restored corpus still
// behaves like the one that wrote it.
func TestControlStateSurvivesThePersistenceEncoding(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)

	// Populate every part the adapter has to carry: an archived conversation
	// with an access snapshot, an operation ledger and a retraction, plus a
	// second conversation that was tombstoned.
	h.index(t, "op-encode-index", "room-1", "scope-room-1", 1, "alpha", "beta", "gamma")
	h.archive(t, "op-encode-archive", "room-1", 1, 1)
	if _, err := h.service.UpdateConversationAccess(ctx, UpdateConversationAccessRequest{
		OperationID: "op-encode-access", ConversationID: "room-1",
		AccessRevision: 1, LifecycleRevision: 1,
		GrantedScopeIDs: []string{"scope-room-1", "scope-team"},
	}); err != nil {
		t.Fatalf("update access: %v", err)
	}
	if _, err := h.service.RetractMessage(ctx, RetractMessageRequest{
		OperationID: "op-encode-retract", ConversationID: "room-1", MessageID: "beta",
		RetractRevision: 1, LifecycleRevision: 1,
	}); err != nil {
		t.Fatalf("retract message: %v", err)
	}
	h.index(t, "op-encode-index-2", "room-2", "scope-room-2", 1, "delta")
	deleted, err := h.service.DeleteConversation(ctx, DeleteConversationRequest{
		OperationID: "op-encode-delete", ConversationID: "room-2", LifecycleRevision: 2,
	})
	if err != nil {
		t.Fatalf("delete conversation: %v", err)
	}

	state, err := h.store.Load(ctx)
	if err != nil {
		t.Fatalf("load control state: %v", err)
	}
	// The encoding boundary is only worth testing against a state that has
	// something in it; an empty state would round-trip for the wrong reason.
	// Three messages remain because deleting a conversation drops its message
	// records and keeps only the tombstone.
	if len(state.Conversations) != 2 || len(state.Messages) != 3 || len(state.Operations) == 0 {
		t.Fatalf("control state is not rich enough to test the encoding: %+v", state)
	}
	payload, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("encode chat control state: %v", err)
	}
	var decoded ControlState
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("decode chat control state: %v", err)
	}
	// Generation is its own column in PostgreSQL, so it is not part of the
	// payload and has to be restored from the row.
	decoded.Generation = state.Generation
	normalizeControlState(&decoded)
	if err := validateControlState(decoded); err != nil {
		t.Fatalf("restored control state failed validation: %v", err)
	}
	if !reflect.DeepEqual(decoded, state) {
		t.Fatalf("control state changed across the persistence encoding:\nstored:  %+v\nrestored:%+v", state, decoded)
	}

	// A corpus restored from that encoding must behave like the original: same
	// hits, same reconciliation view, same idempotency ledger, same fence.
	restored := newHarnessFromState(t, decoded)
	h.indexer.copyInto(restored.indexer)

	before := h.search(t, "gamma", []string{"scope-room-1"}, nil)
	if len(before.Hits) != 1 {
		t.Fatalf("baseline search returned %d hits, want 1", len(before.Hits))
	}
	if after := restored.search(t, "gamma", []string{"scope-room-1"}, nil); !reflect.DeepEqual(after, before) {
		t.Fatalf("search after restore = %+v, want %+v", after, before)
	}
	for _, query := range []string{"beta", "delta"} {
		if hits := restored.search(t, query, []string{"scope-room-1", "scope-room-2"}, nil).Hits; len(hits) != 0 {
			t.Fatalf("restored corpus returned %d hits for %q, want 0 (retracted or tombstoned)", len(hits), query)
		}
	}
	for _, conversationID := range []string{"room-1", "room-2"} {
		before, err := h.service.GetConversationIndexState(ctx, GetConversationIndexStateRequest{ConversationID: conversationID})
		if err != nil {
			t.Fatalf("original conversation state: %v", err)
		}
		after, err := restored.service.GetConversationIndexState(ctx, GetConversationIndexStateRequest{ConversationID: conversationID})
		if err != nil {
			t.Fatalf("restored conversation state: %v", err)
		}
		if !reflect.DeepEqual(after, before) {
			t.Fatalf("conversation %s state after restore = %+v, want %+v", conversationID, after, before)
		}
	}

	// The ledger survived the encoding: replaying the recorded delete returns
	// the recorded result and must not delete anything again.
	deletes := len(restored.indexer.deleted)
	replayed, err := restored.service.DeleteConversation(ctx, DeleteConversationRequest{
		OperationID: "op-encode-delete", ConversationID: "room-2", LifecycleRevision: 2,
	})
	if err != nil || replayed != deleted {
		t.Fatalf("replayed delete = %+v error=%v, want %+v", replayed, err, deleted)
	}
	if got := len(restored.indexer.deleted); got != deletes {
		t.Fatalf("replayed delete removed %d message indexes, want 0", got-deletes)
	}

	// The tombstone fence survived too: the restored corpus must still refuse a
	// late event at the revision the delete already closed.
	if _, err := restored.service.IndexMessages(ctx, IndexMessagesRequest{
		OperationID: "op-encode-late", ConversationID: "room-2", OwnerScopeID: "scope-room-2",
		LifecycleRevision: 1,
		Messages: []MessageInput{{
			MessageID: "late", SenderID: "sender", SentAtUnixMs: 1_700_000_000_000, Content: "late",
		}},
	}); !errors.Is(err, ErrStaleLifecycle) {
		t.Fatalf("late index after restore error = %v, want ErrStaleLifecycle", err)
	}
}

// newHarnessFromState builds a corpus whose control store already holds the
// given state, which is what a restart against PostgreSQL sees.
func newHarnessFromState(t *testing.T, state ControlState) *harness {
	t.Helper()
	indexer := newFakeIndexer()
	searcher := &fakeSearcher{indexer: indexer}
	store := &MemoryControlStore{state: state, domain: "chat-memory-restored"}
	service, err := NewIndexService(context.Background(), IndexServiceConfig{
		ControlStore: store,
		Indexer:      indexer,
		Projection:   indexer,
		Searcher:     searcher,
	})
	if err != nil {
		t.Fatalf("new restored chat index service: %v", err)
	}
	return &harness{service: service, indexer: indexer, searcher: searcher, store: store}
}

// copyInto gives a restored corpus the candidates the original one indexed. The
// fake indexer stands in for the vector store, which in a restart would still
// hold the chunks.
func (f *fakeIndexer) copyInto(target *fakeIndexer) {
	f.mu.Lock()
	defer f.mu.Unlock()
	target.mu.Lock()
	defer target.mu.Unlock()
	for storageID, message := range f.messages {
		target.messages[storageID] = message
	}
}
