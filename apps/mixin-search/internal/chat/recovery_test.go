package chat

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"
	"time"
)

// TestIndexRetryAfterAFailedWriteIsAccepted covers the recovery path an
// adversarial review found broken.
//
// When the vector write fails, the intent is fenced into a durable pending
// delete; when the physical delete fails too, the failure is swallowed by
// design, so the storage id stays on the delete list. Retrying the operation is
// exactly what the contract prescribes, and the retry derives the same storage
// id - which used to claim it again while the delete claim was still there. The
// validator refuses a state that carries one id as both pending write and
// pending delete, so the retry answered with "invalid chat control state"
// wrapped as Unavailable and made no progress at all.
//
// The delete keeps failing here on purpose: while deletes still fail there is
// nothing the retry's maintenance pass can do about the stale claim, so the
// claim has to supersede it. (When deletes work again, the reload before phase
// one clears it anyway, which is why the failure looked intermittent.)
func TestIndexRetryAfterAFailedWriteIsAccepted(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	request := IndexMessagesRequest{
		OperationID: "op-retry", ConversationID: "room-retry", OwnerScopeID: "scope-retry",
		LifecycleRevision: 1,
		Messages: []MessageInput{{
			MessageID: "m1", SenderID: "sender", SentAtUnixMs: 1_700_000_000_000, Content: "retryneedle",
		}},
	}
	storage := storageID(h.service.StorageDomain(), request.ConversationID, "m1", request.OperationID)

	// The whole collection is unavailable: the write fails and so does the
	// cleanup of whatever it may have written.
	h.indexer.indexErr = errors.New("chat collection unavailable")
	h.indexer.deleteErr = errors.New("chat deletion unavailable")
	if _, err := h.service.IndexMessages(ctx, request); err == nil {
		t.Fatal("index succeeded while the chat collection was unavailable")
	}
	state, err := h.store.Load(ctx)
	if err != nil {
		t.Fatalf("load control state: %v", err)
	}
	if !state.PendingDeletes[storage] {
		t.Fatalf("failed write left %q out of the pending deletes: %+v", storage, state.PendingDeletes)
	}

	// Writes work again while deletion is still broken. The retry the contract
	// prescribes must be accepted: it rewrites the very chunks the stale delete
	// was aiming at, and a failed retry would put the delete claim back.
	h.indexer.indexErr = nil
	states, err := h.service.IndexMessages(ctx, request)
	if err != nil {
		t.Fatalf("retry of an idempotent index: %v", err)
	}
	if len(states) != 1 || states[0].MessageID != "m1" {
		t.Fatalf("retry states = %+v, want one state for m1", states)
	}
	state, err = h.store.Load(ctx)
	if err != nil {
		t.Fatalf("load control state after the retry: %v", err)
	}
	if state.PendingDeletes[storage] || state.PendingWrites[storage].OperationID != "" {
		t.Fatalf("retry left the storage id claimed: deletes=%v writes=%v", state.PendingDeletes, state.PendingWrites)
	}

	// And the retry really indexed the message: it becomes retrievable once the
	// conversation is archived.
	h.indexer.record(storage, request.ConversationID, "m1", "retryneedle")
	h.archive(t, "op-retry-archive", request.ConversationID, 1, 1)
	if hits := h.search(t, "retryneedle", []string{"scope-retry"}, nil).Hits; len(hits) != 1 {
		t.Fatalf("hits after the retry = %d, want 1", len(hits))
	}
}

// TestOperationIDRebindingIsRejectedWhileTheIntentIsPending covers the window
// the ledger cannot see: after the intent commit and before the published state
// an operation exists only as a pending write, and the storage ids a retry
// derives change with the messages it carries, so without an explicit check the
// same operation id could be accepted twice with different contents.
func TestOperationIDRebindingIsRejectedWhileTheIntentIsPending(t *testing.T) {
	ctx := context.Background()
	const domain = "chat-memory-pending"
	first := IndexMessagesRequest{
		OperationID: "op-pending", ConversationID: "room-pending", OwnerScopeID: "scope-pending",
		LifecycleRevision: 1,
		Messages: []MessageInput{{
			MessageID: "m1", SenderID: "sender", SentAtUnixMs: 1, Content: "alpha",
		}},
	}
	// The state an index call leaves behind when it dies between phase one and
	// phase three: the intent is durable, the ledger entry is not.
	normalized := []MessageInput{{
		MessageID: "m1", SenderID: "sender", SentAtUnixMs: 1, Content: "alpha",
		ContentSHA256: fmt.Sprintf("%x", sha256.Sum256([]byte("alpha"))),
	}}
	fingerprint := operationFingerprint(struct {
		ConversationID    string
		OwnerScopeID      string
		LifecycleRevision uint64
		Messages          []MessageInput
	}{first.ConversationID, first.OwnerScopeID, first.LifecycleRevision, normalized})
	storage := storageID(domain, first.ConversationID, "m1", first.OperationID)
	state := newControlState()
	state.Generation = 1
	state.PendingWrites[storage] = ControlPendingWrite{
		OperationID:             first.OperationID,
		Fingerprint:             normalized[0].ContentSHA256,
		OperationFingerprint:    fingerprint,
		LeaseExpiresAtUnixMilli: now().Add(pendingWriteLease).UnixMilli(),
	}
	store := &MemoryControlStore{state: state, domain: domain}
	indexer := newFakeIndexer()
	service, err := NewIndexService(ctx, IndexServiceConfig{
		ControlStore: store,
		Indexer:      indexer,
		Projection:   indexer,
		Searcher:     &fakeSearcher{indexer: indexer},
	})
	if err != nil {
		t.Fatalf("new chat index service: %v", err)
	}

	rebound := first
	rebound.Messages = []MessageInput{{
		MessageID: "m2", SenderID: "sender", SentAtUnixMs: 2, Content: "beta",
	}}
	if _, err := service.IndexMessages(ctx, rebound); !errors.Is(err, ErrConflict) {
		t.Fatalf("rebinding an unfinished operation_id error = %v, want ErrConflict", err)
	}

	// The identical retry is still the prescribed behaviour and must succeed.
	if _, err := service.IndexMessages(ctx, first); err != nil {
		t.Fatalf("identical retry of an unfinished operation: %v", err)
	}
}

// TestStaleDeleteRevisionDoesNotOutliveAResurrection covers a cached delete
// result answering for a conversation that is live again: archiving at a higher
// lifecycle revision resurrects a tombstoned conversation, and replaying the
// closed revision afterwards must be refused as stale rather than reported as a
// tombstone.
func TestStaleDeleteRevisionDoesNotOutliveAResurrection(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.index(t, "op-resurrect-index", "room-resurrect", "scope-resurrect", 1, "resurrectneedle")
	h.archive(t, "op-resurrect-archive", "room-resurrect", 1, 1)
	if _, err := h.service.DeleteConversation(ctx, DeleteConversationRequest{
		OperationID: "op-resurrect-delete", ConversationID: "room-resurrect", LifecycleRevision: 2,
	}); err != nil {
		t.Fatalf("delete conversation: %v", err)
	}
	if _, err := h.service.ArchiveConversation(ctx, ArchiveConversationRequest{
		OperationID: "op-resurrect-archive-2", ConversationID: "room-resurrect",
		ArchiveRevision: 2, LifecycleRevision: 3,
	}); err != nil {
		t.Fatalf("resurrect conversation: %v", err)
	}
	h.index(t, "op-resurrect-index-2", "room-resurrect", "scope-resurrect", 3, "resurrectneedle")
	if hits := h.search(t, "resurrectneedle", []string{"scope-resurrect"}, nil).Hits; len(hits) != 1 {
		t.Fatalf("hits after resurrection = %d, want 1", len(hits))
	}

	if _, err := h.service.DeleteConversation(ctx, DeleteConversationRequest{
		OperationID: "op-resurrect-delete-stale", ConversationID: "room-resurrect", LifecycleRevision: 2,
	}); !errors.Is(err, ErrStaleLifecycle) {
		t.Fatalf("delete at a resurrected revision error = %v, want ErrStaleLifecycle", err)
	}
}

// TestOneRequestRejectsADuplicatedMessageId pins the batch contract: a message
// is keyed by (conversation_id, message_id), so repeating an id inside one
// request is ambiguous rather than idempotent.
func TestOneRequestRejectsADuplicatedMessageId(t *testing.T) {
	h := newHarness(t)
	_, err := h.service.IndexMessages(context.Background(), IndexMessagesRequest{
		OperationID: "op-duplicate", ConversationID: "room-duplicate", OwnerScopeID: "scope-duplicate",
		LifecycleRevision: 1,
		Messages: []MessageInput{
			{MessageID: "m1", SenderID: "sender", SentAtUnixMs: 1, Content: "alpha"},
			{MessageID: "m1", SenderID: "sender", SentAtUnixMs: 2, Content: "alpha"},
		},
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("duplicated message_id error = %v, want ErrInvalidInput", err)
	}
}

// TestStorageIDsAreUnambiguous keeps the vector key injective: a separator join
// would map ("a/b", "c") and ("a", "b/c") onto one id, so two unrelated messages
// would replace each other's chunks.
func TestStorageIDsAreUnambiguous(t *testing.T) {
	if left, right := storageID("domain", "a/b", "c", "op"), storageID("domain", "a", "b/c", "op"); left == right {
		t.Fatalf("ambiguous storage ids: %q", left)
	}
	if left, right := storageID("domain", "room", "m/1", "op"), storageID("domain", "room", "m", "1/op"); left == right {
		t.Fatalf("ambiguous storage ids: %q", left)
	}
}

// TestProjectionIntervalReachesTheReconciler covers a configuration knob that was
// documented but never read, so a configured fallback interval silently stayed at
// the default. The interval itself only bounds how long an unnoticed change can
// stay unconverged, which is why this checks the value the reconciler receives
// rather than timing it.
func TestProjectionIntervalReachesTheReconciler(t *testing.T) {
	newService := func(t *testing.T, interval time.Duration) *IndexService {
		t.Helper()
		indexer := newFakeIndexer()
		service, err := NewIndexService(context.Background(), IndexServiceConfig{
			ControlStore:       NewMemoryControlStore(),
			Indexer:            indexer,
			Projection:         indexer,
			Searcher:           &fakeSearcher{indexer: indexer},
			ProjectionInterval: interval,
		})
		if err != nil {
			t.Fatalf("new chat index service: %v", err)
		}
		return service
	}

	configured := 7 * time.Millisecond
	if got := newService(t, configured).projectionInterval; got != configured {
		t.Fatalf("reconciler interval = %v, want %v", got, configured)
	}
	// An unset or non-positive interval must fall back to the default rather than
	// reaching time.NewTicker as zero.
	for _, interval := range []time.Duration{0, -time.Second} {
		if got := newService(t, interval).projectionInterval; got != defaultProjectionInterval {
			t.Fatalf("interval %v produced %v, want the default %v", interval, got, defaultProjectionInterval)
		}
	}
}
