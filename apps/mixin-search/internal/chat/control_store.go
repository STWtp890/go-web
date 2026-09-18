package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// ControlStore persists one complete, generation-guarded chat control snapshot
// per namespace. It is the chat corpus's own storage: the document control plane
// has an equivalent port over a different namespace, and the two never share a
// row, a generation or a payload.
type ControlStore interface {
	Load(context.Context) (ControlState, error)
	Generation(context.Context) (uint64, error)
	Save(context.Context, uint64, ControlState) (uint64, error)
	StorageDomain() string
}

// SizingControlStore is a ControlStore that reports how large the snapshot it
// last wrote was.
//
// The write path is a whole-snapshot rewrite, so a corpus can only be kept
// bounded if something measures the snapshot. Measuring it here is free: the
// persistent adapter already serialized the payload to write it, and the memory
// adapter serializes it for the same reason. The admission guard reads this value
// instead of encoding the candidate state a second time, which at 50k messages
// would add roughly 340 ms to every write.
type SizingControlStore interface {
	ControlStore
	// LastSnapshotBytes reports the encoded size of the last successfully
	// persisted snapshot, or 0 before the first write.
	LastSnapshotBytes() int64
}

// ControlState is the chat corpus's transport-independent persistence model.
type ControlState struct {
	Generation     uint64                         `json:"-"`
	Conversations  map[string]ControlConversation `json:"conversations"`
	Messages       map[string]ControlMessage      `json:"messages"`
	Operations     map[string]ControlOperation    `json:"operations"`
	PendingWrites  map[string]ControlPendingWrite `json:"pending_writes"`
	PendingDeletes map[string]bool                `json:"pending_deletes"`
}

// ControlConversation holds the conversation-level decisions. The four revisions
// are independent high-water marks: advancing one never advances another.
type ControlConversation struct {
	OwnerScopeID      string                  `json:"owner_scope_id"`
	Archived          bool                    `json:"archived"`
	ArchiveRevision   uint64                  `json:"archive_revision"`
	AccessRevision    uint64                  `json:"access_revision"`
	GrantedScopeIDs   []string                `json:"granted_scope_ids"`
	LifecycleRevision uint64                  `json:"lifecycle_revision"`
	TombstoneRevision uint64                  `json:"tombstone_revision"`
	Tombstoned        bool                    `json:"tombstoned"`
	DeleteResults     map[uint64]DeleteResult `json:"delete_results"`
}

// ControlMessage is one immutable indexed message.
type ControlMessage struct {
	ConversationID    string            `json:"conversation_id"`
	MessageID         string            `json:"message_id"`
	SenderID          string            `json:"sender_id"`
	SentAtUnixMs      int64             `json:"sent_at_unix_ms"`
	ContentSHA256     string            `json:"content_sha256"`
	StorageID         string            `json:"storage_id"`
	ChunkCount        int               `json:"chunk_count"`
	LifecycleRevision uint64            `json:"lifecycle_revision"`
	Retracted         bool              `json:"retracted"`
	RetractRevision   uint64            `json:"retract_revision"`
	Metadata          map[string]string `json:"metadata,omitempty"`
}

// ControlPendingWrite is an unfinished vector write intent with a lease.
//
// Fingerprint is the message's content digest, which binds one storage id to
// one payload. OperationFingerprint is the digest of the whole request, which
// binds the operation id while the intent is still pending: the ledger only
// records finished operations, so without it a retry after a crash could reuse
// an operation id with a different batch, because the storage ids it derives
// change with the message ids. It is empty for intents written before that field
// existed, and an empty value is treated as unknown rather than as a mismatch.
type ControlPendingWrite struct {
	OperationID             string `json:"operation_id"`
	Fingerprint             string `json:"fingerprint"`
	OperationFingerprint    string `json:"operation_fingerprint,omitempty"`
	LeaseExpiresAtUnixMilli int64  `json:"lease_expires_at_unix_milli"`
}

// ControlOperationResult is a typed union: exactly one field is present and it
// must match the operation kind, which preserves the first response across a
// restart without persisting protocol DTOs.
type ControlOperationResult struct {
	Messages []MessageState `json:"messages,omitempty"`
	Archive  *ArchiveResult `json:"archive,omitempty"`
	Access   *AccessState   `json:"access,omitempty"`
	Retract  *RetractResult `json:"retract,omitempty"`
	Delete   *DeleteResult  `json:"delete,omitempty"`
}

// ControlOperation is one idempotency ledger entry.
//
// RecordedAtUnixMilli is when the operation was accepted. It exists so the
// ledger can be pruned by age (ADR-015): an entry without a timestamp predates
// that field and is kept until the entry-count ceiling decides otherwise.
type ControlOperation struct {
	Kind                string                 `json:"kind"`
	Fingerprint         string                 `json:"fingerprint"`
	RecordedAtUnixMilli int64                  `json:"recorded_at_unix_milli,omitempty"`
	Result              ControlOperationResult `json:"result"`
}

const (
	operationIndexMessages = "index_messages"
	operationArchive       = "archive_conversation"
	operationUpdateAccess  = "update_conversation_access"
	operationRetract       = "retract_message"
	operationDelete        = "delete_conversation"
)

func newControlState() ControlState {
	return ControlState{
		Conversations:  make(map[string]ControlConversation),
		Messages:       make(map[string]ControlMessage),
		Operations:     make(map[string]ControlOperation),
		PendingWrites:  make(map[string]ControlPendingWrite),
		PendingDeletes: make(map[string]bool),
	}
}

func normalizeControlState(state *ControlState) {
	if state.Conversations == nil {
		state.Conversations = make(map[string]ControlConversation)
	}
	if state.Messages == nil {
		state.Messages = make(map[string]ControlMessage)
	}
	if state.Operations == nil {
		state.Operations = make(map[string]ControlOperation)
	}
	if state.PendingWrites == nil {
		state.PendingWrites = make(map[string]ControlPendingWrite)
	}
	if state.PendingDeletes == nil {
		state.PendingDeletes = make(map[string]bool)
	}
	for conversationID, conversation := range state.Conversations {
		if conversation.DeleteResults == nil {
			conversation.DeleteResults = make(map[uint64]DeleteResult)
		}
		state.Conversations[conversationID] = conversation
	}
}

// validateControlState rejects a snapshot whose invariants do not hold. It runs
// once per load, so an incomplete snapshot fails closed at startup instead of
// being guessed at.
func validateControlState(state ControlState) error {
	for key, message := range state.Messages {
		if message.ConversationID == "" || message.MessageID == "" || message.StorageID == "" {
			return fmt.Errorf("invalid chat control state: message %q is incomplete", key)
		}
		if key != messageKey(message.ConversationID, message.MessageID) {
			return fmt.Errorf("invalid chat control state: message %q has a mismatched natural key", key)
		}
		conversation, ok := state.Conversations[message.ConversationID]
		if !ok || conversation.OwnerScopeID == "" {
			return fmt.Errorf("invalid chat control state: message %q has no owning conversation", key)
		}
		if message.Retracted && message.RetractRevision == 0 {
			return fmt.Errorf("invalid chat control state: message %q is retracted without a revision", key)
		}
	}
	for conversationID, conversation := range state.Conversations {
		if conversationID == "" {
			return errors.New("invalid chat control state: empty conversation key")
		}
		// A conversation this corpus never indexed may still hold a tombstone: a
		// delete for an unknown conversation is idempotent and must be replayable.
		// Anything else without an owner scope carries no information and must not
		// be persisted.
		if conversation.OwnerScopeID == "" && !conversation.Tombstoned {
			return fmt.Errorf("invalid chat control state: conversation %q has no owner scope", conversationID)
		}
		// An archived conversation is retrievable, so it must not still be a
		// tombstone: resurrecting requires a higher lifecycle revision and an
		// explicit archive decision.
		if !equalStrings(normalizeIDs(conversation.GrantedScopeIDs), conversation.GrantedScopeIDs) {
			return fmt.Errorf("invalid chat control state: conversation %q has a non-normalized access snapshot", conversationID)
		}
		if conversation.Tombstoned {
			if conversation.Archived || conversation.TombstoneRevision == 0 ||
				conversation.LifecycleRevision < conversation.TombstoneRevision {
				return fmt.Errorf("invalid chat control state: conversation %q has an inconsistent tombstone", conversationID)
			}
		}
		if conversation.Archived && (conversation.OwnerScopeID == "" || conversation.ArchiveRevision == 0) {
			return fmt.Errorf("invalid chat control state: conversation %q is archived without an owner scope and revision", conversationID)
		}
	}
	for storage, pending := range state.PendingDeletes {
		if storage == "" || !pending {
			return fmt.Errorf("invalid chat control state: pending delete %q is invalid", storage)
		}
	}
	for storage, pending := range state.PendingWrites {
		if storage == "" || pending.OperationID == "" || pending.Fingerprint == "" || pending.LeaseExpiresAtUnixMilli <= 0 {
			return fmt.Errorf("invalid chat control state: pending write %q is incomplete", storage)
		}
		if state.PendingDeletes[storage] {
			return fmt.Errorf("invalid chat control state: vector %q is pending write and delete", storage)
		}
	}
	for operationID, operation := range state.Operations {
		if operationID == "" || operation.Fingerprint == "" {
			return fmt.Errorf("invalid chat control state: operation %q is incomplete", operationID)
		}
		if err := validateOperation(operation); err != nil {
			return fmt.Errorf("invalid chat control state: operation %q: %w", operationID, err)
		}
	}
	return nil
}

func validateOperation(operation ControlOperation) error {
	count := 0
	if operation.Result.Messages != nil {
		count++
	}
	if operation.Result.Archive != nil {
		count++
	}
	if operation.Result.Access != nil {
		count++
	}
	if operation.Result.Retract != nil {
		count++
	}
	if operation.Result.Delete != nil {
		count++
	}
	if count != 1 {
		return errors.New("operation result must contain exactly one typed value")
	}
	switch operation.Kind {
	case operationIndexMessages:
		if operation.Result.Messages == nil {
			return fmt.Errorf("operation kind %q requires message states", operation.Kind)
		}
	case operationArchive:
		if operation.Result.Archive == nil {
			return fmt.Errorf("operation kind %q requires an archive result", operation.Kind)
		}
	case operationUpdateAccess:
		if operation.Result.Access == nil {
			return fmt.Errorf("operation kind %q requires an access state", operation.Kind)
		}
	case operationRetract:
		if operation.Result.Retract == nil {
			return fmt.Errorf("operation kind %q requires a retraction result", operation.Kind)
		}
	case operationDelete:
		if operation.Result.Delete == nil {
			return fmt.Errorf("operation kind %q requires a delete result", operation.Kind)
		}
	default:
		return fmt.Errorf("unknown operation kind %q", operation.Kind)
	}
	return nil
}

func cloneControlState(state ControlState) (ControlState, error) {
	payload, err := json.Marshal(state)
	if err != nil {
		return ControlState{}, fmt.Errorf("marshal chat control state: %w", err)
	}
	var clone ControlState
	if err := json.Unmarshal(payload, &clone); err != nil {
		return ControlState{}, fmt.Errorf("unmarshal chat control state clone: %w", err)
	}
	clone.Generation = state.Generation
	normalizeControlState(&clone)
	if err := validateControlState(clone); err != nil {
		return ControlState{}, err
	}
	return clone, nil
}
