// Package chat implements the chat-corpus control plane.
//
// It is a separate control plane from the document one on purpose: its own
// snapshot type, its own generation, its own persistence namespace and its own
// projection reconciler (see docs/adr/014-per-corpus-control-plane-isolation.md).
// The machinery for publishing snapshots and converging a projection is shared
// through internal/controlplane; the state is not.
//
// Ownership: py-agent owns conversations, messages, QQ identities and channel
// permissions. This package owns only rebuildable chat index state. It never
// creates, edits or deletes an archived chat record.
package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	// ErrInvalidInput reports a request that violates the contract shape.
	ErrInvalidInput = errors.New("invalid chat index input")
	// ErrNotFound reports an unknown conversation or message.
	ErrNotFound = errors.New("chat entity not found")
	// ErrConflict reports a payload bound to an existing key, or an immutable
	// message whose content changed.
	ErrConflict = errors.New("chat index conflict")
	// ErrStaleLifecycle reports an event below the conversation lifecycle
	// high-water mark, including anything arriving after a tombstone.
	ErrStaleLifecycle = errors.New("stale chat conversation lifecycle revision")
	// ErrStaleArchive reports an archive decision below the archive high-water mark.
	ErrStaleArchive = errors.New("stale chat archive revision")
	// ErrStaleAccess reports an access snapshot below the access high-water mark.
	ErrStaleAccess = errors.New("stale chat access revision")
	// ErrStaleRetract reports a retraction below the message retraction high-water mark.
	ErrStaleRetract = errors.New("stale chat retraction revision")
	// ErrControlStoreConflict reports another instance committing a newer
	// generation; the caller re-reads and retries with the same operation id.
	ErrControlStoreConflict = errors.New("chat control store generation conflict")
	// ErrControlStoreUnavailable marks this instance failed closed.
	ErrControlStoreUnavailable = errors.New("chat control store unavailable")
	// ErrProjectionUnavailable reports that the derived chat index could not be
	// converged, so retrieval cannot serve the snapshot it was asked about.
	ErrProjectionUnavailable = errors.New("chat index projection unavailable")
)

// IndexStatus is the conversation-level retrievability decision. It deliberately
// has no "stored" value: storing messages belongs to py-agent.
type IndexStatus string

const (
	// StatusIndexed means messages were accepted but the conversation is not
	// retrievable yet.
	StatusIndexed IndexStatus = "INDEXED"
	// StatusArchived means the conversation is eligible for retrieval.
	StatusArchived IndexStatus = "ARCHIVED"
)

// MessageInput is one immutable message as submitted by py-agent.
type MessageInput struct {
	MessageID     string
	SenderID      string
	SentAtUnixMs  int64
	Content       string
	ContentSHA256 string
	Metadata      map[string]string
}

// MessageState is the indexed state of one message.
type MessageState struct {
	ConversationID    string
	MessageID         string
	OwnerScopeID      string
	SenderID          string
	SentAtUnixMs      int64
	ArchiveRevision   uint64
	AccessRevision    uint64
	LifecycleRevision uint64
	RetractRevision   uint64
	Retracted         bool
	ChunkCount        int
	ContentSHA256     string
}

// AccessState is the current access snapshot of one conversation.
type AccessState struct {
	ConversationID    string
	AccessRevision    uint64
	LifecycleRevision uint64
	GrantedScopeIDs   []string
}

// ArchiveResult reports the outcome of an archive decision.
type ArchiveResult struct {
	Status            IndexStatus
	ArchiveRevision   uint64
	LifecycleRevision uint64
}

// RetractResult reports the outcome of a retraction.
type RetractResult struct {
	Retracted       bool
	RetractRevision uint64
}

// DeleteResult reports the outcome of deleting a conversation's derived index.
type DeleteResult struct {
	Tombstoned        bool
	LifecycleRevision uint64
}

// ConversationIndexState is the reconciliation view of one conversation.
type ConversationIndexState struct {
	Exists                bool
	Status                IndexStatus
	OwnerScopeID          string
	ArchiveRevision       uint64
	AccessRevision        uint64
	LifecycleRevision     uint64
	TombstoneRevision     uint64
	Tombstoned            bool
	IndexedMessageCount   int
	RetractedMessageCount int
}

// IndexMessagesRequest indexes a batch of immutable messages.
type IndexMessagesRequest struct {
	OperationID       string
	ConversationID    string
	OwnerScopeID      string
	LifecycleRevision uint64
	Messages          []MessageInput
}

// ArchiveConversationRequest marks a conversation as retrievable.
type ArchiveConversationRequest struct {
	OperationID       string
	ConversationID    string
	ArchiveRevision   uint64
	LifecycleRevision uint64
}

// UpdateConversationAccessRequest replaces the complete access snapshot.
type UpdateConversationAccessRequest struct {
	OperationID       string
	ConversationID    string
	AccessRevision    uint64
	LifecycleRevision uint64
	GrantedScopeIDs   []string
}

// RetractMessageRequest removes one message from retrieval.
type RetractMessageRequest struct {
	OperationID       string
	ConversationID    string
	MessageID         string
	RetractRevision   uint64
	LifecycleRevision uint64
}

// DeleteConversationRequest tombstones a conversation and drops its index.
type DeleteConversationRequest struct {
	OperationID       string
	ConversationID    string
	LifecycleRevision uint64
}

// GetConversationIndexStateRequest asks for one conversation's state.
type GetConversationIndexStateRequest struct {
	ConversationID string
}

// SearchMessagesRequest is a chat retrieval request.
type SearchMessagesRequest struct {
	Query                  string
	AllowedScopeIDs        []string
	AllowedConversationIDs []string
	SentAfterUnixMs        int64
	SentBeforeUnixMs       int64
	TopK                   int
}

// ScoredMessageChunk is one candidate returned by the vector store.
type ScoredMessageChunk struct {
	ConversationID string
	MessageID      string
	StorageID      string
	Position       int
	Snippet        string
	Score          float64
}

// MessageHit is one authorized search result.
type MessageHit struct {
	ConversationID string
	MessageID      string
	OwnerScopeID   string
	SenderID       string
	SentAtUnixMs   int64
	Position       int
	Snippet        string
	ContentSHA256  string
	Score          float64
}

// SearchMessagesResult is the retrieval result.
type SearchMessagesResult struct {
	Query     string
	Truncated bool
	Hits      []MessageHit
}

// MessageIndexer writes message chunks into this corpus's vector collection and
// removes them again. It is the only path from this control plane to a vector
// store, which is what keeps the chat collection independent of the document one.
type MessageIndexer interface {
	IndexMessage(ctx context.Context, storageID string, message MessageInput) (chunkCount int, err error)
	DeleteMessage(ctx context.Context, storageID string) error
}

// ProjectionStore materializes chat lifecycle and authorization state into the
// vector store so candidate selection can filter on it.
type ProjectionStore interface {
	SyncChatControls(ctx context.Context, controls []VectorControl) error
}

// Searcher recalls chat candidates. The control plane then applies the
// retrievability rules, so a store that returns too much can never leak.
type Searcher interface {
	SearchMessages(ctx context.Context, query string, limit int) ([]ScoredMessageChunk, error)
}

// VectorControl is the chat projection attached to indexed message chunks.
type VectorControl struct {
	StorageID         string
	StorageDomain     string
	ConversationID    string
	MessageID         string
	OwnerScopeID      string
	GrantedScopeIDs   []string
	Archived          bool
	Retracted         bool
	Tombstoned        bool
	ArchiveRevision   uint64
	AccessRevision    uint64
	LifecycleRevision uint64
	ContentSHA256     string
}

// now is indirect so tests can drive time-dependent behaviour.
var now = time.Now

func normalizeIDs(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, duplicate := seen[trimmed]; duplicate {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	sortStrings(result)
	return result
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func cloneStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	return append([]string(nil), values...)
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func messageKey(conversationID, messageID string) string {
	return conversationID + "\x00" + messageID
}

func storageID(domain, conversationID, messageID, operationID string) string {
	return fmt.Sprintf("%s/%s/%s/%s", domain, conversationID, messageID, operationID)
}
