package chat

import (
	"context"
	"reflect"
	"testing"
	"time"
)

// TestOperationLedgerRetentionKeepsTheWindowAndForgetsBeyondIt walks the window
// ADR-015 defines. Inside it a replay is answered from the ledger; outside it the
// ledger no longer answers, but the corpus still does not write anything twice.
func TestOperationLedgerRetentionKeepsTheWindowAndForgetsBeyondIt(t *testing.T) {
	ctx := context.Background()
	clock := time.Unix(1_760_000_000, 0).UTC()
	restoreClock := setClock(t, &clock)
	defer restoreClock()

	h := newLimitedHarness(t, IndexServiceConfig{OperationRetention: time.Hour})
	indexRequest := IndexMessagesRequest{
		OperationID: "op-retention", ConversationID: "room-retention", OwnerScopeID: "scope-retention",
		LifecycleRevision: 1,
		Messages: []MessageInput{{
			MessageID: "m1", SenderID: "sender", SentAtUnixMs: 1_700_000_000_000, Content: "retentionneedle",
		}},
	}
	if _, err := h.service.IndexMessages(ctx, indexRequest); err != nil {
		t.Fatalf("index: %v", err)
	}
	vectorsAfterFirst := h.indexer.indexedCount()

	// Inside the window the ledger answers, and nothing new is written.
	replayed, err := h.service.IndexMessages(ctx, indexRequest)
	if err != nil {
		t.Fatalf("replay inside the window: %v", err)
	}
	if len(replayed) != 1 || replayed[0].MessageID != "m1" {
		t.Fatalf("replay inside the window = %+v, want the first response", replayed)
	}
	if h.indexer.indexedCount() != vectorsAfterFirst {
		t.Fatalf("replay inside the window wrote vectors: %d -> %d", vectorsAfterFirst, h.indexer.indexedCount())
	}

	// Past the window the entry is pruned by the next control-plane write.
	clock = clock.Add(2 * time.Hour)
	if err := h.service.pruneOperationLedger(ctx, h.service.state.Load()); err != nil {
		t.Fatalf("prune ledger: %v", err)
	}
	state, err := h.store.Load(ctx)
	if err != nil {
		t.Fatalf("load control state: %v", err)
	}
	if _, stillThere := state.Operations["op-retention"]; stillThere {
		t.Fatalf("expired operation survived the retention window: %+v", state.Operations)
	}

	// Outside the window the ledger no longer answers, but the write is still not
	// duplicated: the vector key contains the operation id, so the replay rewrites
	// the same key instead of adding a second message. Accepting it also records
	// the operation again, which restores the conflict check from then on.
	if _, err := h.service.IndexMessages(ctx, indexRequest); err != nil {
		t.Fatalf("replay outside the window: %v", err)
	}
	if h.indexer.indexedCount() != vectorsAfterFirst {
		t.Fatalf("replay outside the window duplicated vectors: %d -> %d", vectorsAfterFirst, h.indexer.indexedCount())
	}
	conversationState, err := h.service.GetConversationIndexState(ctx, GetConversationIndexStateRequest{
		ConversationID: "room-retention",
	})
	if err != nil {
		t.Fatalf("conversation state: %v", err)
	}
	if conversationState.IndexedMessageCount != 1 {
		t.Fatalf("indexed messages = %d, want 1 after a replay outside the window", conversationState.IndexedMessageCount)
	}
	state, err = h.store.Load(ctx)
	if err != nil {
		t.Fatalf("load control state: %v", err)
	}
	if _, reRecorded := state.Operations["op-retention"]; !reRecorded {
		t.Fatal("a write accepted outside the window did not record its operation again")
	}
}

// TestOperationLedgerRebindingOutsideTheWindowIsAccepted pins the cost ADR-015
// accepts explicitly. The order matters: this is the case where the entry is gone
// and the *first* thing that arrives under that operation id is a different
// payload, so there is nothing left to compare against.
func TestOperationLedgerRebindingOutsideTheWindowIsAccepted(t *testing.T) {
	ctx := context.Background()
	clock := time.Unix(1_760_000_000, 0).UTC()
	restoreClock := setClock(t, &clock)
	defer restoreClock()

	h := newLimitedHarness(t, IndexServiceConfig{OperationRetention: time.Hour})
	h.index(t, "op-rebound", "room-rebound", "scope-rebound", 1, "first")

	clock = clock.Add(2 * time.Hour)
	if err := h.service.pruneOperationLedger(ctx, h.service.state.Load()); err != nil {
		t.Fatalf("prune ledger: %v", err)
	}
	if _, err := h.service.IndexMessages(ctx, IndexMessagesRequest{
		OperationID: "op-rebound", ConversationID: "room-rebound", OwnerScopeID: "scope-rebound",
		LifecycleRevision: 1,
		Messages: []MessageInput{{
			MessageID: "second", SenderID: "sender", SentAtUnixMs: 1_700_000_000_001, Content: "second",
		}},
	}); err != nil {
		t.Fatalf("rebinding outside the window error = %v, want acceptance (ADR-015)", err)
	}
}

// TestOperationLedgerCeilingDropsOldestFirst covers the backstop: with a count
// ceiling and no retention window, the oldest entries go first.
func TestOperationLedgerCeilingDropsOldestFirst(t *testing.T) {
	ctx := context.Background()
	clock := time.Unix(1_760_000_000, 0).UTC()
	restoreClock := setClock(t, &clock)
	defer restoreClock()

	h := newLimitedHarness(t, IndexServiceConfig{MaxOperationEntries: 2})
	for index, content := range []string{"one", "two", "three", "four"} {
		clock = clock.Add(time.Minute)
		h.index(t, "op-ceiling-"+content, "room-ceiling", "scope-ceiling", 1, content)
		_ = index
	}
	if err := h.service.pruneOperationLedger(ctx, h.service.state.Load()); err != nil {
		t.Fatalf("prune ledger: %v", err)
	}
	state, err := h.store.Load(ctx)
	if err != nil {
		t.Fatalf("load control state: %v", err)
	}
	if len(state.Operations) != 2 {
		t.Fatalf("ledger holds %d entries, want 2", len(state.Operations))
	}
	for _, kept := range []string{"op-ceiling-three", "op-ceiling-four"} {
		if _, ok := state.Operations[kept]; !ok {
			t.Fatalf("entry %q was dropped before older ones: %+v", kept, state.Operations)
		}
	}
}

// TestOperationLedgerRetentionDisabledChangesNothing keeps the default honest:
// with both settings at zero the ledger is never pruned.
func TestOperationLedgerRetentionDisabledChangesNothing(t *testing.T) {
	ctx := context.Background()
	h := newLimitedHarness(t, IndexServiceConfig{})
	h.index(t, "op-keep-1", "room-keep", "scope-keep", 1, "one")
	h.index(t, "op-keep-2", "room-keep", "scope-keep", 1, "two")
	if err := h.service.pruneOperationLedger(ctx, h.service.state.Load()); err != nil {
		t.Fatalf("prune ledger: %v", err)
	}
	state, err := h.store.Load(ctx)
	if err != nil {
		t.Fatalf("load control state: %v", err)
	}
	if len(state.Operations) != 2 {
		t.Fatalf("ledger holds %d entries, want both kept", len(state.Operations))
	}
}

// TestOperationLedgerPruningFreesSnapshotSpace keeps the ledger and the snapshot
// budget connected: pruning must be visible to the capacity guard.
func TestOperationLedgerPruningFreesSnapshotSpace(t *testing.T) {
	ctx := context.Background()
	clock := time.Unix(1_760_000_000, 0).UTC()
	restoreClock := setClock(t, &clock)
	defer restoreClock()

	h := newLimitedHarness(t, IndexServiceConfig{OperationRetention: time.Minute})
	h.index(t, "op-space-1", "room-space", "scope-space", 1, "one")
	before := h.store.LastSnapshotBytes()

	clock = clock.Add(2 * time.Minute)
	if err := h.service.pruneOperationLedger(ctx, h.service.state.Load()); err != nil {
		t.Fatalf("prune ledger: %v", err)
	}
	if after := h.store.LastSnapshotBytes(); after >= before {
		t.Fatalf("snapshot did not shrink after pruning the ledger: %d -> %d", before, after)
	}
}

// TestExpiredLedgerReplayIsIdempotentPerRPC is the business-semantics test the map
// assertions cannot give: once an operation's ledger entry has expired, every RPC
// must still be idempotent in *effect* when the same operation_id arrives again.
//
// The property asserted is deliberately narrow and checkable: the semantic state
// (conversations, messages, pending work) is unchanged by the replay, and the
// replay does not add a second vector. The response itself is allowed to be
// recomputed, which is exactly what the documented window gives up.
func TestExpiredLedgerReplayIsIdempotentPerRPC(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name     string
		prepare  func(t *testing.T, h *harness, clock *time.Time)
		replay   func(t *testing.T, h *harness)
		describe string
	}{
		{
			name: "IndexMessages",
			prepare: func(t *testing.T, h *harness, clock *time.Time) {
				h.index(t, "op-expire-index", "room-expire", "scope-expire", 1, "first")
			},
			replay: func(t *testing.T, h *harness) {
				if _, err := h.service.IndexMessages(context.Background(), IndexMessagesRequest{
					OperationID: "op-expire-index", ConversationID: "room-expire", OwnerScopeID: "scope-expire",
					LifecycleRevision: 1,
					Messages: []MessageInput{{
						MessageID: "first", SenderID: "sender", SentAtUnixMs: 1_700_000_000_000, Content: "first",
					}},
				}); err != nil {
					t.Fatalf("replay expired IndexMessages: %v", err)
				}
			},
			describe: "the same message is re-indexed onto the same storage key, not added twice",
		},
		{
			name: "ArchiveConversation",
			prepare: func(t *testing.T, h *harness, clock *time.Time) {
				h.index(t, "op-expire-archive-index", "room-expire", "scope-expire", 1, "first")
				h.archive(t, "op-expire-archive", "room-expire", 1, 1)
			},
			replay: func(t *testing.T, h *harness) {
				if _, err := h.service.ArchiveConversation(context.Background(), ArchiveConversationRequest{
					OperationID: "op-expire-archive", ConversationID: "room-expire",
					ArchiveRevision: 1, LifecycleRevision: 1,
				}); err != nil {
					t.Fatalf("replay expired ArchiveConversation: %v", err)
				}
			},
			describe: "re-archiving at the same revision is a no-op on state",
		},
		{
			name: "UpdateConversationAccess",
			prepare: func(t *testing.T, h *harness, clock *time.Time) {
				h.index(t, "op-expire-access-index", "room-expire", "scope-expire", 1, "first")
				h.archive(t, "op-expire-access-archive", "room-expire", 1, 1)
				if _, err := h.service.UpdateConversationAccess(context.Background(), UpdateConversationAccessRequest{
					OperationID: "op-expire-access", ConversationID: "room-expire",
					AccessRevision: 1, LifecycleRevision: 1, GrantedScopeIDs: []string{"scope-expire"},
				}); err != nil {
					t.Fatalf("update access: %v", err)
				}
			},
			replay: func(t *testing.T, h *harness) {
				if _, err := h.service.UpdateConversationAccess(context.Background(), UpdateConversationAccessRequest{
					OperationID: "op-expire-access", ConversationID: "room-expire",
					AccessRevision: 1, LifecycleRevision: 1, GrantedScopeIDs: []string{"scope-expire"},
				}); err != nil {
					t.Fatalf("replay expired UpdateConversationAccess: %v", err)
				}
			},
			describe: "re-applying the same access snapshot is a no-op on state",
		},
		{
			name: "RetractMessage",
			prepare: func(t *testing.T, h *harness, clock *time.Time) {
				h.index(t, "op-expire-retract-index", "room-expire", "scope-expire", 1, "first")
				h.archive(t, "op-expire-retract-archive", "room-expire", 1, 1)
				if _, err := h.service.RetractMessage(context.Background(), RetractMessageRequest{
					OperationID: "op-expire-retract", ConversationID: "room-expire", MessageID: "first",
					RetractRevision: 1, LifecycleRevision: 1,
				}); err != nil {
					t.Fatalf("retract message: %v", err)
				}
			},
			replay: func(t *testing.T, h *harness) {
				if _, err := h.service.RetractMessage(context.Background(), RetractMessageRequest{
					OperationID: "op-expire-retract", ConversationID: "room-expire", MessageID: "first",
					RetractRevision: 1, LifecycleRevision: 1,
				}); err != nil {
					t.Fatalf("replay expired RetractMessage: %v", err)
				}
			},
			describe: "re-retracting at the same revision is a no-op on state",
		},
		{
			name: "DeleteConversation",
			prepare: func(t *testing.T, h *harness, clock *time.Time) {
				h.index(t, "op-expire-delete-index", "room-expire", "scope-expire", 1, "first")
				h.archive(t, "op-expire-delete-archive", "room-expire", 1, 1)
				if _, err := h.service.DeleteConversation(context.Background(), DeleteConversationRequest{
					OperationID: "op-expire-delete", ConversationID: "room-expire", LifecycleRevision: 2,
				}); err != nil {
					t.Fatalf("delete conversation: %v", err)
				}
			},
			replay: func(t *testing.T, h *harness) {
				if _, err := h.service.DeleteConversation(context.Background(), DeleteConversationRequest{
					OperationID: "op-expire-delete", ConversationID: "room-expire", LifecycleRevision: 2,
				}); err != nil {
					t.Fatalf("replay expired DeleteConversation: %v", err)
				}
			},
			describe: "the tombstone revision answers the replay; the tombstone stays a tombstone",
		},
	}

	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			clock := time.Unix(1_760_000_000, 0).UTC()
			restoreClock := setClock(t, &clock)
			defer restoreClock()

			h := newLimitedHarness(t, IndexServiceConfig{OperationRetention: time.Hour})
			testCase.prepare(t, h, &clock)

			// Expire every ledger entry, so the replay cannot be answered from it.
			clock = clock.Add(48 * time.Hour)
			if err := h.service.pruneOperationLedger(ctx, h.service.state.Load()); err != nil {
				t.Fatalf("prune ledger: %v", err)
			}
			stateBefore, err := h.store.Load(ctx)
			if err != nil {
				t.Fatalf("load state before replay: %v", err)
			}
			if len(stateBefore.Operations) != 0 {
				t.Fatalf("ledger still holds %d entries after the window expired", len(stateBefore.Operations))
			}
			vectorsBefore := h.indexer.indexedCount()

			testCase.replay(t, h)

			stateAfter, err := h.store.Load(ctx)
			if err != nil {
				t.Fatalf("load state after replay: %v", err)
			}
			if vectors := h.indexer.indexedCount(); vectors != vectorsBefore {
				t.Fatalf(
					"%s: replaying an expired operation changed the vector count %d -> %d (%s)",
					testCase.name, vectorsBefore, vectors, testCase.describe,
				)
			}
			assertSameSemanticState(t, testCase.name, stateBefore, stateAfter)
		})
	}
}

// assertSameSemanticState compares everything except the generation (a replay may
// legitimately commit) and the ledger (the replay is allowed to record itself
// again, which is what restores rebinding detection).
func assertSameSemanticState(t *testing.T, label string, before, after ControlState) {
	t.Helper()
	if !reflect.DeepEqual(before.Conversations, after.Conversations) {
		t.Fatalf("%s: conversations changed across an expired-operation replay:\nbefore=%+v\nafter=%+v",
			label, before.Conversations, after.Conversations)
	}
	if !reflect.DeepEqual(before.Messages, after.Messages) {
		t.Fatalf("%s: messages changed across an expired-operation replay:\nbefore=%+v\nafter=%+v",
			label, before.Messages, after.Messages)
	}
	if !reflect.DeepEqual(before.PendingWrites, after.PendingWrites) ||
		!reflect.DeepEqual(before.PendingDeletes, after.PendingDeletes) {
		t.Fatalf("%s: pending work changed across an expired-operation replay", label)
	}
}

// setClock replaces the package clock and returns a restore function, so a test
// can move the retention window without sleeping.
func setClock(t *testing.T, at *time.Time) func() {
	t.Helper()
	previous := now
	now = func() time.Time { return *at }
	t.Cleanup(func() { now = previous })
	return func() { now = previous }
}
