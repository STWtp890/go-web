package chat

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
)

// snapshot is the chat corpus's immutable in-memory control state. The service
// publishes a new one atomically and never mutates a published snapshot, so
// readers need no lock. Sharing a snapshot with the document corpus is exactly
// what ADR-014 forbids, which is why this type is separate even though the
// publication mechanism is shared.
type snapshot struct {
	generation        uint64
	conversations     map[string]*conversationState
	messages          map[string]*messageState
	messagesByStorage map[string]string
	operations        map[string]operationRecord
	pendingWrites     map[string]ControlPendingWrite
	pendingDeletes    map[string]struct{}
}

type conversationState struct {
	ownerScopeID      string
	archived          bool
	archiveRevision   uint64
	accessRevision    uint64
	grantedScopeIDs   []string
	lifecycleRevision uint64
	tombstoneRevision uint64
	tombstoned        bool
	deleteResults     map[uint64]DeleteResult
}

type messageState struct {
	conversationID    string
	messageID         string
	senderID          string
	sentAtUnixMs      int64
	contentSHA256     string
	storageID         string
	chunkCount        int
	lifecycleRevision uint64
	retracted         bool
	retractRevision   uint64
	metadata          map[string]string
}

type operationRecord struct {
	kind        string
	fingerprint string
	result      any
}

func newSnapshot() *snapshot {
	return &snapshot{
		conversations:     make(map[string]*conversationState),
		messages:          make(map[string]*messageState),
		messagesByStorage: make(map[string]string),
		operations:        make(map[string]operationRecord),
		pendingWrites:     make(map[string]ControlPendingWrite),
		pendingDeletes:    make(map[string]struct{}),
	}
}

func (s *snapshot) clone() *snapshot {
	clone := &snapshot{
		generation:        s.generation,
		conversations:     make(map[string]*conversationState, len(s.conversations)),
		messages:          make(map[string]*messageState, len(s.messages)),
		messagesByStorage: make(map[string]string, len(s.messagesByStorage)),
		operations:        make(map[string]operationRecord, len(s.operations)),
		pendingWrites:     make(map[string]ControlPendingWrite, len(s.pendingWrites)),
		pendingDeletes:    make(map[string]struct{}, len(s.pendingDeletes)),
	}
	for conversationID, conversation := range s.conversations {
		copied := *conversation
		copied.grantedScopeIDs = cloneStrings(conversation.grantedScopeIDs)
		copied.deleteResults = make(map[uint64]DeleteResult, len(conversation.deleteResults))
		for revision, result := range conversation.deleteResults {
			copied.deleteResults[revision] = result
		}
		clone.conversations[conversationID] = &copied
	}
	for key, message := range s.messages {
		copied := *message
		copied.metadata = cloneStringMap(message.metadata)
		clone.messages[key] = &copied
	}
	for storage, key := range s.messagesByStorage {
		clone.messagesByStorage[storage] = key
	}
	for operationID, record := range s.operations {
		copied := record
		copied.result = cloneOperationResult(record.result)
		clone.operations[operationID] = copied
	}
	for storage, pending := range s.pendingWrites {
		clone.pendingWrites[storage] = pending
	}
	for storage := range s.pendingDeletes {
		clone.pendingDeletes[storage] = struct{}{}
	}
	return clone
}

// conversation returns the conversation state, creating an empty one in this
// snapshot when the conversation is new. Callers must hold a writer-private copy
// whenever the result may be mutated.
//
// Creation is a side effect, so read paths must use lookup instead: an empty
// conversation carries no information and the persistence model rejects it.
func (s *snapshot) conversation(conversationID string) *conversationState {
	conversation := s.conversations[conversationID]
	if conversation == nil {
		conversation = &conversationState{deleteResults: make(map[uint64]DeleteResult)}
		s.conversations[conversationID] = conversation
	}
	return conversation
}

// lookup returns the conversation state without creating one.
func (s *snapshot) lookup(conversationID string) *conversationState {
	return s.conversations[conversationID]
}

// needsMaintenance reports whether this snapshot still requires work only a
// control-plane write can finish. A write intent whose lease is still valid
// belongs to an in-flight index call, so it is deliberately not maintenance:
// treating it as such would make every concurrent reader queue behind a writer.
func (s *snapshot) needsMaintenance(nowUnixMilli int64) bool {
	if len(s.pendingDeletes) > 0 {
		return true
	}
	for _, pending := range s.pendingWrites {
		if pending.LeaseExpiresAtUnixMilli <= nowUnixMilli {
			return true
		}
	}
	return false
}

// replayOperation returns the first response of an already accepted operation,
// and rejects an operation id rebound to a different payload.
func (s *snapshot) replayOperation(operationID, kind, fingerprint string) (any, bool, error) {
	record, ok := s.operations[operationID]
	if !ok {
		return nil, false, nil
	}
	if record.kind != kind || record.fingerprint != fingerprint {
		return nil, false, fmt.Errorf("%w: operation_id %q is already bound to %s", ErrConflict, operationID, record.kind)
	}
	return cloneOperationResult(record.result), true, nil
}

func (s *snapshot) recordOperation(operationID, kind, fingerprint string, result any) {
	s.operations[operationID] = operationRecord{
		kind:        kind,
		fingerprint: fingerprint,
		result:      cloneOperationResult(result),
	}
}

// vectorControls renders the projection the chat collection needs for candidate
// selection. A tombstoned conversation projects its indexed messages as
// tombstones, and retracted messages project as retracted rather than being
// silently dropped, so the store can never return them.
func (s *snapshot) vectorControls(storageDomain string) []VectorControl {
	controls := make([]VectorControl, 0, len(s.messages))
	for _, message := range s.messages {
		conversation := s.conversations[message.conversationID]
		if conversation == nil {
			continue
		}
		controls = append(controls, VectorControl{
			StorageID:         message.storageID,
			StorageDomain:     storageDomain,
			ConversationID:    message.conversationID,
			MessageID:         message.messageID,
			OwnerScopeID:      conversation.ownerScopeID,
			GrantedScopeIDs:   cloneStrings(conversation.grantedScopeIDs),
			Archived:          conversation.archived && !conversation.tombstoned,
			Retracted:         message.retracted,
			Tombstoned:        conversation.tombstoned,
			ArchiveRevision:   conversation.archiveRevision,
			AccessRevision:    conversation.accessRevision,
			LifecycleRevision: conversation.lifecycleRevision,
			ContentSHA256:     message.contentSHA256,
		})
	}
	for storage := range s.pendingDeletes {
		if _, live := s.messagesByStorage[storage]; live {
			continue
		}
		controls = append(controls, VectorControl{
			StorageID:     storage,
			StorageDomain: storageDomain,
			Tombstoned:    true,
		})
	}
	return controls
}

func (s *snapshot) toControlState() ControlState {
	state := newControlState()
	state.Generation = s.generation
	for conversationID, conversation := range s.conversations {
		deleteResults := make(map[uint64]DeleteResult, len(conversation.deleteResults))
		for revision, result := range conversation.deleteResults {
			deleteResults[revision] = result
		}
		state.Conversations[conversationID] = ControlConversation{
			OwnerScopeID:      conversation.ownerScopeID,
			Archived:          conversation.archived,
			ArchiveRevision:   conversation.archiveRevision,
			AccessRevision:    conversation.accessRevision,
			GrantedScopeIDs:   cloneStrings(conversation.grantedScopeIDs),
			LifecycleRevision: conversation.lifecycleRevision,
			TombstoneRevision: conversation.tombstoneRevision,
			Tombstoned:        conversation.tombstoned,
			DeleteResults:     deleteResults,
		}
	}
	for key, message := range s.messages {
		state.Messages[key] = ControlMessage{
			ConversationID:    message.conversationID,
			MessageID:         message.messageID,
			SenderID:          message.senderID,
			SentAtUnixMs:      message.sentAtUnixMs,
			ContentSHA256:     message.contentSHA256,
			StorageID:         message.storageID,
			ChunkCount:        message.chunkCount,
			LifecycleRevision: message.lifecycleRevision,
			Retracted:         message.retracted,
			RetractRevision:   message.retractRevision,
			Metadata:          cloneStringMap(message.metadata),
		}
	}
	for operationID, record := range s.operations {
		operation, err := controlOperationFromRecord(record)
		if err != nil {
			// The snapshot only ever holds records this package produced, so this
			// cannot happen; persisting the zero operation would be worse.
			panic(fmt.Sprintf("render chat operation %q: %v", operationID, err))
		}
		state.Operations[operationID] = operation
	}
	for storage := range s.pendingDeletes {
		state.PendingDeletes[storage] = true
	}
	for storage, pending := range s.pendingWrites {
		state.PendingWrites[storage] = pending
	}
	return state
}

func snapshotFromControlState(state ControlState) (*snapshot, error) {
	state, err := cloneControlState(state)
	if err != nil {
		return nil, err
	}
	built := newSnapshot()
	built.generation = state.Generation
	for conversationID, conversation := range state.Conversations {
		deleteResults := make(map[uint64]DeleteResult, len(conversation.DeleteResults))
		for revision, result := range conversation.DeleteResults {
			deleteResults[revision] = result
		}
		built.conversations[conversationID] = &conversationState{
			ownerScopeID:      conversation.OwnerScopeID,
			archived:          conversation.Archived,
			archiveRevision:   conversation.ArchiveRevision,
			accessRevision:    conversation.AccessRevision,
			grantedScopeIDs:   cloneStrings(conversation.GrantedScopeIDs),
			lifecycleRevision: conversation.LifecycleRevision,
			tombstoneRevision: conversation.TombstoneRevision,
			tombstoned:        conversation.Tombstoned,
			deleteResults:     deleteResults,
		}
	}
	for key, message := range state.Messages {
		built.messages[key] = &messageState{
			conversationID:    message.ConversationID,
			messageID:         message.MessageID,
			senderID:          message.SenderID,
			sentAtUnixMs:      message.SentAtUnixMs,
			contentSHA256:     message.ContentSHA256,
			storageID:         message.StorageID,
			chunkCount:        message.ChunkCount,
			lifecycleRevision: message.LifecycleRevision,
			retracted:         message.Retracted,
			retractRevision:   message.RetractRevision,
			metadata:          cloneStringMap(message.Metadata),
		}
		built.messagesByStorage[message.StorageID] = key
	}
	for operationID, operation := range state.Operations {
		result, err := controlOperationValue(operation)
		if err != nil {
			return nil, fmt.Errorf("restore chat operation %q: %w", operationID, err)
		}
		built.operations[operationID] = operationRecord{
			kind:        operation.Kind,
			fingerprint: operation.Fingerprint,
			result:      result,
		}
	}
	for storage := range state.PendingDeletes {
		built.pendingDeletes[storage] = struct{}{}
	}
	for storage, pending := range state.PendingWrites {
		built.pendingWrites[storage] = pending
	}
	return built, nil
}

func controlOperationFromRecord(record operationRecord) (ControlOperation, error) {
	operation := ControlOperation{Kind: record.kind, Fingerprint: record.fingerprint}
	switch result := record.result.(type) {
	case []MessageState:
		operation.Result.Messages = append([]MessageState(nil), result...)
	case ArchiveResult:
		copied := result
		operation.Result.Archive = &copied
	case AccessState:
		copied := result
		copied.GrantedScopeIDs = cloneStrings(result.GrantedScopeIDs)
		operation.Result.Access = &copied
	case RetractResult:
		copied := result
		operation.Result.Retract = &copied
	case DeleteResult:
		copied := result
		operation.Result.Delete = &copied
	default:
		return ControlOperation{}, fmt.Errorf("unsupported chat operation result %T", record.result)
	}
	if err := validateOperation(operation); err != nil {
		return ControlOperation{}, err
	}
	return operation, nil
}

func controlOperationValue(operation ControlOperation) (any, error) {
	if err := validateOperation(operation); err != nil {
		return nil, err
	}
	switch operation.Kind {
	case operationIndexMessages:
		return append([]MessageState(nil), operation.Result.Messages...), nil
	case operationArchive:
		return *operation.Result.Archive, nil
	case operationUpdateAccess:
		return cloneAccessState(*operation.Result.Access), nil
	case operationRetract:
		return *operation.Result.Retract, nil
	case operationDelete:
		return *operation.Result.Delete, nil
	default:
		return nil, fmt.Errorf("unknown operation kind %q", operation.Kind)
	}
}

func cloneOperationResult(result any) any {
	switch value := result.(type) {
	case []MessageState:
		return append([]MessageState(nil), value...)
	case AccessState:
		return cloneAccessState(value)
	default:
		return value
	}
}

func cloneAccessState(state AccessState) AccessState {
	state.GrantedScopeIDs = cloneStrings(state.GrantedScopeIDs)
	return state
}

// operationFingerprint binds an operation id to one exact payload, so a retry
// with the same id and payload replays and a different payload is rejected.
func operationFingerprint(value any) string {
	payload, err := json.Marshal(value)
	if err != nil {
		// The values here are plain structs of strings and numbers.
		panic(fmt.Sprintf("encode chat operation fingerprint: %v", err))
	}
	digest := sha256.Sum256(payload)
	return fmt.Sprintf("sha256:%x", digest)
}
