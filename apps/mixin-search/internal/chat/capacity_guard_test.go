package chat

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

// TestCapacityGuardRefusesWritesPastTheMessageLimit covers the hard limit the
// capacity measurement exists to justify: past it the corpus refuses new messages
// instead of growing until every writer times out.
func TestCapacityGuardRefusesWritesPastTheMessageLimit(t *testing.T) {
	ctx := context.Background()
	h := newLimitedHarness(t, IndexServiceConfig{MaxMessages: 2})

	h.index(t, "op-capacity-1", "room-limit", "scope-limit", 1, "first", "second")

	// The batch that would cross the limit is refused outright, and nothing of it
	// is written.
	if _, err := h.service.IndexMessages(ctx, IndexMessagesRequest{
		OperationID: "op-capacity-2", ConversationID: "room-limit", OwnerScopeID: "scope-limit",
		LifecycleRevision: 1,
		Messages: []MessageInput{{
			MessageID: "third", SenderID: "sender", SentAtUnixMs: 1_700_000_000_002, Content: "third",
		}},
	}); !errors.Is(err, ErrCapacityExceeded) {
		t.Fatalf("write past the message limit error = %v, want ErrCapacityExceeded", err)
	}
	if h.indexer.indexedCount() != 2 {
		t.Fatalf("indexed vectors = %d, want 2: a refused batch must not be written", h.indexer.indexedCount())
	}

	// The corpus keeps serving what it has: a refused write is not an outage.
	h.archive(t, "op-capacity-archive", "room-limit", 1, 1)
	if hits := h.search(t, "first", []string{"scope-limit"}, nil).Hits; len(hits) != 1 {
		t.Fatalf("hits after a refused write = %d, want 1", len(hits))
	}

	// Replaying the accepted operation adds nothing, so it still succeeds.
	if _, err := h.service.IndexMessages(ctx, IndexMessagesRequest{
		OperationID: "op-capacity-1", ConversationID: "room-limit", OwnerScopeID: "scope-limit",
		LifecycleRevision: 1,
		Messages: []MessageInput{
			{MessageID: "first", SenderID: "sender", SentAtUnixMs: 1_700_000_000_000, Content: "first"},
			{MessageID: "second", SenderID: "sender", SentAtUnixMs: 1_700_000_000_001, Content: "second"},
		},
	}); err != nil {
		t.Fatalf("replay at the limit: %v", err)
	}
}

// TestCapacityGuardRefusesOnceTheSnapshotLimitIsReached covers the second hard
// limit. It is checked against the last persisted snapshot, which is why the
// guard can lag by one write - stated here so nobody reads it as exact.
func TestCapacityGuardRefusesOnceTheSnapshotLimitIsReached(t *testing.T) {
	ctx := context.Background()
	h := newLimitedHarness(t, IndexServiceConfig{MaxSnapshotBytes: 200})

	h.index(t, "op-snapshot-1", "room-snapshot", "scope-snapshot", 1, "first")
	if got := h.store.LastSnapshotBytes(); got <= 200 {
		t.Fatalf("persisted snapshot = %d bytes, expected the guard's threshold to be crossed", got)
	}
	if _, err := h.service.IndexMessages(ctx, IndexMessagesRequest{
		OperationID: "op-snapshot-2", ConversationID: "room-snapshot", OwnerScopeID: "scope-snapshot",
		LifecycleRevision: 1,
		Messages: []MessageInput{{
			MessageID: "second", SenderID: "sender", SentAtUnixMs: 1_700_000_000_001, Content: "second",
		}},
	}); !errors.Is(err, ErrCapacityExceeded) {
		t.Fatalf("write past the snapshot limit error = %v, want ErrCapacityExceeded", err)
	}
}

// TestZeroCapacityLimitsDoNotRestrictWrites keeps the default honest: until the
// measured limits are confirmed and configured, behaviour is unchanged.
func TestZeroCapacityLimitsDoNotRestrictWrites(t *testing.T) {
	h := newLimitedHarness(t, IndexServiceConfig{})
	h.index(t, "op-unlimited-1", "room-unlimited", "scope-unlimited", 1, "first")
	h.index(t, "op-unlimited-2", "room-unlimited", "scope-unlimited", 1, "second", "third")
	if h.indexer.indexedCount() != 3 {
		t.Fatalf("indexed vectors = %d, want 3", h.indexer.indexedCount())
	}
}

// TestNegativeCapacityLimitsAreRejected stops a typo in configuration from
// silently disabling the guard it was meant to enable.
func TestNegativeCapacityLimitsAreRejected(t *testing.T) {
	indexer := newFakeIndexer()
	for name, limits := range map[string]IndexServiceConfig{
		"negative messages":  {MaxMessages: -1},
		"negative bytes":     {MaxSnapshotBytes: -1},
		"negative retention": {OperationRetention: -time.Second},
		"negative ceiling":   {MaxOperationEntries: -1},
		// A sub-millisecond window truncates to zero in the age comparison, which
		// would look enabled while pruning nothing.
		"sub-millisecond retention": {OperationRetention: time.Microsecond},
	} {
		limits := limits
		t.Run(name, func(t *testing.T) {
			service, err := NewIndexService(context.Background(), IndexServiceConfig{
				ControlStore:        NewMemoryControlStore(),
				Indexer:             indexer,
				Projection:          indexer,
				Searcher:            &fakeSearcher{indexer: indexer},
				MaxMessages:         limits.MaxMessages,
				MaxSnapshotBytes:    limits.MaxSnapshotBytes,
				OperationRetention:  limits.OperationRetention,
				MaxOperationEntries: limits.MaxOperationEntries,
			})
			if err == nil {
				t.Fatal("a negative capacity limit was accepted")
			}
			if service != nil {
				t.Fatal("a service was returned alongside the error")
			}
		})
	}
}

// TestSnapshotCeilingRefusesEveryMutationNotJustIndexing pins the rule an
// operator gets: at the ceiling the corpus refuses writes and keeps serving
// reads. Enforcing the ceiling on indexing alone would not bound the snapshot at
// all, because archive, access, retraction and deletion each add a ledger entry.
func TestSnapshotCeilingRefusesEveryMutationNotJustIndexing(t *testing.T) {
	ctx := context.Background()
	h := newLimitedHarness(t, IndexServiceConfig{MaxSnapshotBytes: 400})
	h.index(t, "op-ceiling-index", "room-ceiling-all", "scope-ceiling-all", 1, "first")

	// Everything a refusal must leave alone: the durable semantic state, the
	// generation, the pending work and the vector store.
	before, err := h.store.Load(ctx)
	if err != nil {
		t.Fatalf("load state before the refusals: %v", err)
	}
	vectorsBefore := h.indexer.indexedCount()

	if _, err := h.service.ArchiveConversation(ctx, ArchiveConversationRequest{
		OperationID: "op-ceiling-archive", ConversationID: "room-ceiling-all",
		ArchiveRevision: 1, LifecycleRevision: 1,
	}); !errors.Is(err, ErrCapacityExceeded) {
		t.Fatalf("archive at the ceiling error = %v, want ErrCapacityExceeded", err)
	}
	if _, err := h.service.UpdateConversationAccess(ctx, UpdateConversationAccessRequest{
		OperationID: "op-ceiling-access", ConversationID: "room-ceiling-all",
		AccessRevision: 1, LifecycleRevision: 1, GrantedScopeIDs: []string{"scope-ceiling-all"},
	}); !errors.Is(err, ErrCapacityExceeded) {
		t.Fatalf("access at the ceiling error = %v, want ErrCapacityExceeded", err)
	}
	if _, err := h.service.RetractMessage(ctx, RetractMessageRequest{
		OperationID: "op-ceiling-retract", ConversationID: "room-ceiling-all", MessageID: "first",
		RetractRevision: 1, LifecycleRevision: 1,
	}); !errors.Is(err, ErrCapacityExceeded) {
		t.Fatalf("retract at the ceiling error = %v, want ErrCapacityExceeded", err)
	}
	if _, err := h.service.DeleteConversation(ctx, DeleteConversationRequest{
		OperationID: "op-ceiling-delete", ConversationID: "room-ceiling-all", LifecycleRevision: 2,
	}); !errors.Is(err, ErrCapacityExceeded) {
		t.Fatalf("delete at the ceiling error = %v, want ErrCapacityExceeded", err)
	}
	if _, err := h.service.IndexMessages(ctx, IndexMessagesRequest{
		OperationID: "op-ceiling-index-2", ConversationID: "room-ceiling-all", OwnerScopeID: "scope-ceiling-all",
		LifecycleRevision: 1,
		Messages: []MessageInput{{
			MessageID: "second", SenderID: "sender", SentAtUnixMs: 1_700_000_000_001, Content: "second",
		}},
	}); !errors.Is(err, ErrCapacityExceeded) {
		t.Fatalf("indexing at the ceiling error = %v, want ErrCapacityExceeded", err)
	}

	// A refused batch must not half-happen: no semantic state, no generation bump,
	// no pending write intent and no vector is allowed to survive it.
	after, err := h.store.Load(ctx)
	if err != nil {
		t.Fatalf("load state after the refusals: %v", err)
	}
	if after.Generation != before.Generation {
		t.Fatalf("a refused write committed: generation %d -> %d", before.Generation, after.Generation)
	}
	if !reflect.DeepEqual(before.Conversations, after.Conversations) ||
		!reflect.DeepEqual(before.Messages, after.Messages) ||
		!reflect.DeepEqual(before.PendingWrites, after.PendingWrites) ||
		!reflect.DeepEqual(before.PendingDeletes, after.PendingDeletes) {
		t.Fatalf(
			"a refused write changed the control state:\nbefore=%+v\nafter=%+v",
			before, after,
		)
	}
	if vectors := h.indexer.indexedCount(); vectors != vectorsBefore {
		t.Fatalf("a refused write wrote vectors: %d -> %d", vectorsBefore, vectors)
	}

	// Refusing mutations must not refuse answers: the recorded operation is still
	// replayed from the ledger, and reads keep working.
	if _, err := h.service.IndexMessages(ctx, IndexMessagesRequest{
		OperationID: "op-ceiling-index", ConversationID: "room-ceiling-all", OwnerScopeID: "scope-ceiling-all",
		LifecycleRevision: 1,
		Messages: []MessageInput{{
			MessageID: "first", SenderID: "sender", SentAtUnixMs: 1_700_000_000_000, Content: "first",
		}},
	}); err != nil {
		t.Fatalf("replay at the ceiling: %v", err)
	}
	if _, err := h.service.GetConversationIndexState(ctx, GetConversationIndexStateRequest{
		ConversationID: "room-ceiling-all",
	}); err != nil {
		t.Fatalf("read at the ceiling: %v", err)
	}
}

// TestSnapshotCeilingSeesASnapshotAnotherInstanceGrew covers the case a
// per-instance counter misses: the row is grown by another instance while this
// one only remembers its own last write. The guard must compare against the
// persisted snapshot, which the reload refreshes.
func TestSnapshotCeilingSeesASnapshotAnotherInstanceGrew(t *testing.T) {
	ctx := context.Background()
	h := newLimitedHarness(t, IndexServiceConfig{MaxSnapshotBytes: 300})
	h.index(t, "op-foreign-index", "room-foreign", "scope-foreign", 1, "first")

	// Stand in for another instance: grow the stored snapshot directly, which also
	// updates the size the store reports.
	current, err := h.store.Load(ctx)
	if err != nil {
		t.Fatalf("load control state: %v", err)
	}
	grown := buildCapacityState(200, 50)
	grown.Generation = current.Generation
	if _, err := h.store.Save(ctx, current.Generation, grown); err != nil {
		t.Fatalf("grow the stored snapshot: %v", err)
	}

	if _, err := h.service.IndexMessages(ctx, IndexMessagesRequest{
		OperationID: "op-foreign-index-2", ConversationID: "room-foreign", OwnerScopeID: "scope-foreign",
		LifecycleRevision: 1,
		Messages: []MessageInput{{
			MessageID: "second", SenderID: "sender", SentAtUnixMs: 1_700_000_000_001, Content: "second",
		}},
	}); !errors.Is(err, ErrCapacityExceeded) {
		t.Fatalf("write after another instance grew the snapshot error = %v, want ErrCapacityExceeded", err)
	}
}

// TestMetadataBudgetIsEnforcedBeforeAnyWrite covers the budget the capacity
// measurements justified: metadata lives inside the snapshot, so an over-budget
// message is a malformed request (InvalidArgument), and the whole batch must be
// rejected before any state is claimed or any vector is written.
func TestMetadataBudgetIsEnforcedBeforeAnyWrite(t *testing.T) {
	ctx := context.Background()
	limits := DefaultMetadataLimits()
	cases := []struct {
		name     string
		metadata map[string]string
	}{
		{
			name:     "too many entries",
			metadata: map[string]string{"a": "1", "b": "2", "c": "3", "d": "4", "e": "5", "f": "6", "g": "7", "h": "8", "i": "9"},
		},
		{
			name:     "key too long",
			metadata: map[string]string{strings.Repeat("k", limits.KeyBytes+1): "v"},
		},
		{
			name:     "value too long",
			metadata: map[string]string{"k": strings.Repeat("v", limits.ValueBytes+1)},
		},
		{
			name: "keys and values over the total",
			metadata: map[string]string{
				strings.Repeat("k", 24): strings.Repeat("v", 24),
				strings.Repeat("j", 24): strings.Repeat("w", 24),
			},
		},
	}
	for _, testCase := range cases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			h := newLimitedHarness(t, IndexServiceConfig{})
			before, err := h.store.Load(ctx)
			if err != nil {
				t.Fatalf("load state before: %v", err)
			}
			_, err = h.service.IndexMessages(ctx, IndexMessagesRequest{
				OperationID: "op-metadata", ConversationID: "room-metadata", OwnerScopeID: "scope-metadata",
				LifecycleRevision: 1,
				Messages: []MessageInput{{
					MessageID: "m1", SenderID: "sender", SentAtUnixMs: 1_700_000_000_000,
					Content: "first", Metadata: testCase.metadata,
				}},
			})
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("over-budget metadata error = %v, want ErrInvalidInput", err)
			}
			// Nothing was claimed and nothing was written: the batch failed
			// validation, not capacity.
			if vectors := h.indexer.indexedCount(); vectors != 0 {
				t.Fatalf("a rejected batch wrote %d vectors", vectors)
			}
			after, err := h.store.Load(ctx)
			if err != nil {
				t.Fatalf("load state after: %v", err)
			}
			if after.Generation != before.Generation ||
				!reflect.DeepEqual(before.Messages, after.Messages) ||
				!reflect.DeepEqual(before.PendingWrites, after.PendingWrites) {
				t.Fatalf("a rejected batch changed the control state: before=%+v after=%+v", before, after)
			}
		})
	}

	// The boundary itself is accepted: a message that fills the budget exactly
	// passes, so the limit is a limit and not an off-by-one.
	t.Run("exactly at the budget is accepted", func(t *testing.T) {
		h := newLimitedHarness(t, IndexServiceConfig{})
		if _, err := h.service.IndexMessages(ctx, IndexMessagesRequest{
			OperationID: "op-metadata-ok", ConversationID: "room-metadata", OwnerScopeID: "scope-metadata",
			LifecycleRevision: 1,
			Messages: []MessageInput{{
				MessageID: "m1", SenderID: "sender", SentAtUnixMs: 1_700_000_000_000,
				Content: "first",
				Metadata: map[string]string{
					strings.Repeat("k", limits.KeyBytes): strings.Repeat("v", limits.TotalBytes-limits.KeyBytes),
				},
			}},
		}); err != nil {
			t.Fatalf("a message exactly at the metadata budget was rejected: %v", err)
		}
	})
}

func newLimitedHarness(t *testing.T, config IndexServiceConfig) *harness {
	t.Helper()
	indexer := newFakeIndexer()
	searcher := &fakeSearcher{indexer: indexer}
	store := NewMemoryControlStore()
	config.ControlStore = store
	config.Indexer = indexer
	config.Projection = indexer
	config.Searcher = searcher
	service, err := NewIndexService(context.Background(), config)
	if err != nil {
		t.Fatalf("new chat index service with limits: %v", err)
	}
	return &harness{service: service, indexer: indexer, searcher: searcher, store: store}
}
