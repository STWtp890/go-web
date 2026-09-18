package chat

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"mixin-search/internal/controlplane"
)

const (
	// pendingWriteLease bounds how long an unfinished vector write may hold its
	// intent before another request fences it into a pending delete.
	pendingWriteLease = 15 * time.Minute

	// defaultProjectionInterval bounds how long a published change can wait for
	// the derived index to catch up when nobody signals.
	defaultProjectionInterval = 200 * time.Millisecond

	// projectionTimeout bounds one background convergence.
	projectionTimeout = 10 * time.Second
)

// IndexService is the chat corpus control plane: it owns the conversation and
// message state machine, its own generation and its own projection. Candidate
// retrieval and vector writes go through ports, so the collection this corpus
// uses is a composition-root decision rather than something the control plane
// can share with documents.
type IndexService struct {
	store           ControlStore
	indexer         MessageIndexer
	projectionStore ProjectionStore
	searcher        Searcher
	storageDomain   string

	state *controlplane.State[snapshot]
	// writeMu serializes control-plane mutations; readers never take it.
	writeMu sync.Mutex
	// reloadMu serializes reloads, so a reader whose snapshot is stale never
	// waits behind a writer's vector I/O.
	reloadMu sync.Mutex

	projection *controlplane.Projection
	// projectionInterval is the reconciler's fallback interval. It is configured,
	// not constant, so a deployment or test can shorten the window in which a
	// published change can stay unconverged.
	projectionInterval time.Duration
	// maxMessages and maxSnapshotBytes are the corpus's hard capacity limits; zero
	// means unbounded.
	maxMessages      int
	maxSnapshotBytes int64
	// operationRetention and maxOperationEntries bound the idempotency ledger
	// (ADR-015): entries older than the retention window, or the oldest entries
	// beyond the count ceiling, are dropped by the maintenance pass. Zero disables
	// each, which is the default until the confirmed limits land.
	operationRetention  time.Duration
	maxOperationEntries int
	// lastSnapshotBytes is the encoded size of the persisted snapshot, used by the
	// snapshot-bytes guard. It is atomic because the opportunistic maintenance pass
	// can commit (and refresh it) from a read path while a writer reads it.
	lastSnapshotBytes atomic.Int64
}

// IndexServiceConfig configures the chat control plane.
type IndexServiceConfig struct {
	// ControlStore is this corpus's own persistent state; required.
	ControlStore ControlStore
	// Indexer writes message chunks into the chat collection; required.
	Indexer MessageIndexer
	// Projection materializes chat lifecycle state into the collection. When it
	// is nil the corpus keeps no projection, which is only valid for tests.
	Projection ProjectionStore
	// Searcher recalls candidates. When it is nil, retrieval fails closed rather
	// than returning an empty result that looks like "nothing matched".
	Searcher Searcher
	// ProjectionInterval overrides the reconciler's fallback interval.
	ProjectionInterval time.Duration
	// StorageDomain overrides the projection domain. It defaults to the control
	// store's domain, and it must be the same value the vector collection was
	// built with: the collection filters candidates on it, so a mismatch would
	// silently hide everything.
	StorageDomain string
	// MaxMessages is this corpus's hard limit on indexed messages. A write that
	// would cross it is refused. Zero disables the limit, which is the current
	// state until the measured limit is confirmed.
	MaxMessages int
	// MaxSnapshotBytes is this corpus's hard limit on the encoded control
	// snapshot. It is checked against the last persisted snapshot, so enforcement
	// can lag by one write; that keeps the guard free instead of encoding the
	// candidate state twice. Zero disables the limit.
	MaxSnapshotBytes int64
	// OperationRetention is how long the idempotency ledger keeps an operation
	// (ADR-015). Zero keeps every entry forever.
	OperationRetention time.Duration
	// MaxOperationEntries is the ceiling on ledger entries, applied after the
	// retention window and oldest-first. Zero means no ceiling.
	MaxOperationEntries int
}

// NewIndexService restores the chat control plane before returning a usable
// service: a load or validation failure prevents startup, so indexed messages
// can never be served under lifecycle state that was never loaded.
func NewIndexService(ctx context.Context, config IndexServiceConfig) (*IndexService, error) {
	if config.ControlStore == nil {
		return nil, errors.New("chat control store is required")
	}
	if config.Indexer == nil {
		return nil, errors.New("chat message indexer is required")
	}
	if ctx == nil {
		return nil, errors.New("chat control store load context is required")
	}
	state, err := config.ControlStore.Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: load chat control state: %w", ErrControlStoreUnavailable, err)
	}
	restored, err := snapshotFromControlState(state)
	if err != nil {
		return nil, fmt.Errorf("%w: restore chat control state: %w", ErrControlStoreUnavailable, err)
	}
	storageDomain := strings.TrimSpace(config.StorageDomain)
	if storageDomain == "" {
		storageDomain = config.ControlStore.StorageDomain()
	}
	if storageDomain == "" {
		return nil, errors.New("chat storage domain is required")
	}
	projectionInterval := config.ProjectionInterval
	if projectionInterval <= 0 {
		projectionInterval = defaultProjectionInterval
	}
	if config.MaxMessages < 0 || config.MaxSnapshotBytes < 0 ||
		config.OperationRetention < 0 || config.MaxOperationEntries < 0 {
		return nil, errors.New("chat capacity limits cannot be negative")
	}
	// A retention shorter than a millisecond would truncate to zero in the age
	// comparison, so the setting would look enabled while pruning nothing. Refuse
	// it instead of silently disabling what an operator asked for.
	if config.OperationRetention > 0 && config.OperationRetention < time.Millisecond {
		return nil, errors.New("chat operation retention must be at least 1ms, or 0 to keep every entry")
	}
	service := &IndexService{
		store:               config.ControlStore,
		indexer:             config.Indexer,
		projectionStore:     config.Projection,
		searcher:            config.Searcher,
		storageDomain:       storageDomain,
		projectionInterval:  projectionInterval,
		maxMessages:         config.MaxMessages,
		maxSnapshotBytes:    config.MaxSnapshotBytes,
		operationRetention:  config.OperationRetention,
		maxOperationEntries: config.MaxOperationEntries,
		state:               controlplane.NewState(restored, restored.generation),
		projection:          controlplane.NewProjection(),
	}
	service.refreshSnapshotBytes()
	return service, nil
}

// refreshSnapshotBytes records how large the persisted snapshot currently is, so
// the capacity guard compares against the row a reload just read and not against
// whatever this instance last wrote. On a shared namespace another instance may
// have grown the snapshot since, and that is exactly the case the limit exists to
// catch.
func (s *IndexService) refreshSnapshotBytes() {
	if sizing, ok := s.store.(SizingControlStore); ok {
		s.lastSnapshotBytes.Store(sizing.LastSnapshotBytes())
	}
}

// StorageDomain reports the projection domain of this corpus. It is the chat
// collection's own domain and never the document one.
func (s *IndexService) StorageDomain() string { return s.storageDomain }

// StartProjectionReconciler keeps the chat projection converged in the
// background until ctx is cancelled. It is separate from the document
// reconciler: neither corpus's changes can trigger the other's convergence.
//
// The callback performs the raw projection write only. Converge owns the locking
// and the synced-generation bookkeeping, so calling it from inside this callback
// would re-enter its own lock.
func (s *IndexService) StartProjectionReconciler(ctx context.Context) {
	s.projection.StartReconciler(ctx, controlplane.ReconcilerConfig{
		Interval: s.projectionInterval,
		Timeout:  projectionTimeout,
		Target:   func() uint64 { return s.state.Generation() },
		Sync: func(syncCtx context.Context, generation uint64) error {
			current := s.state.Load()
			if s.projectionStore == nil || current.generation != generation {
				return nil
			}
			return s.syncProjection(syncCtx, current)
		},
	})
}

// IndexMessages indexes a batch of immutable messages.
func (s *IndexService) IndexMessages(ctx context.Context, request IndexMessagesRequest) ([]MessageState, error) {
	request.OperationID = strings.TrimSpace(request.OperationID)
	request.ConversationID = strings.TrimSpace(request.ConversationID)
	request.OwnerScopeID = strings.TrimSpace(request.OwnerScopeID)
	if request.OperationID == "" || request.ConversationID == "" || request.OwnerScopeID == "" || len(request.Messages) == 0 {
		return nil, fmt.Errorf("%w: operation_id, conversation_id, owner_scope_id and at least one message are required", ErrInvalidInput)
	}
	normalized := make([]MessageInput, 0, len(request.Messages))
	seenMessageIDs := make(map[string]struct{}, len(request.Messages))
	for _, message := range request.Messages {
		message.MessageID = strings.TrimSpace(message.MessageID)
		message.SenderID = strings.TrimSpace(message.SenderID)
		message.ContentSHA256 = strings.ToLower(strings.TrimSpace(message.ContentSHA256))
		if message.MessageID == "" || message.SenderID == "" || strings.TrimSpace(message.Content) == "" {
			return nil, fmt.Errorf("%w: every message requires message_id, sender_id and content", ErrInvalidInput)
		}
		// A message is keyed by (conversation_id, message_id), so repeating an id
		// inside one batch is ambiguous rather than idempotent: it would write the
		// same vectors twice and return the message twice.
		if _, duplicate := seenMessageIDs[message.MessageID]; duplicate {
			return nil, fmt.Errorf("%w: message_id %q appears more than once in one request", ErrInvalidInput, message.MessageID)
		}
		seenMessageIDs[message.MessageID] = struct{}{}
		digest := fmt.Sprintf("%x", sha256.Sum256([]byte(message.Content)))
		if message.ContentSHA256 != "" && message.ContentSHA256 != digest {
			return nil, fmt.Errorf("%w: content_sha256 does not match content for message %q", ErrInvalidInput, message.MessageID)
		}
		message.ContentSHA256 = digest
		message.Metadata = cloneStringMap(message.Metadata)
		normalized = append(normalized, message)
	}
	fingerprint := operationFingerprint(struct {
		ConversationID    string
		OwnerScopeID      string
		LifecycleRevision uint64
		Messages          []MessageInput
	}{request.ConversationID, request.OwnerScopeID, request.LifecycleRevision, normalized})

	locked, err := s.lockWrite(ctx)
	if err != nil {
		return nil, err
	}
	defer s.unlockWrite()
	snapshot := locked
	next := snapshot.clone()

	if replay, ok, err := next.replayOperation(request.OperationID, operationIndexMessages, fingerprint); err != nil {
		return nil, err
	} else if ok {
		return replay.([]MessageState), nil
	}
	// The ledger above only knows finished operations. An operation whose write
	// died between the intent commit and the published state exists only as a
	// pending intent, so rebinding has to be checked there too.
	if err := next.pendingOperationConflict(request.OperationID, fingerprint); err != nil {
		return nil, err
	}
	// Capacity is checked before any state is claimed: the write path is a
	// whole-snapshot rewrite, so the honest answer past the limit is a refusal
	// that leaves the corpus serving what it has, not a corpus that grows until
	// every writer times out.
	if err := s.checkCapacity(next, request.ConversationID, normalized); err != nil {
		return nil, err
	}
	// Phase one: bind an unfinished write intent for every message that is not
	// already indexed, so a crash cannot leave vectors that no intent accounts for.
	// No conversation is created here: the pending write belongs to a message, and
	// a conversation only becomes real once it owns an indexed message.
	conversation := next.lookup(request.ConversationID)
	if conversation != nil && conversation.ownerScopeID != "" && conversation.ownerScopeID != request.OwnerScopeID {
		return nil, fmt.Errorf("%w: conversation %q belongs to scope %q", ErrConflict, request.ConversationID, conversation.ownerScopeID)
	}
	if conversation != nil {
		if err := validateLifecycle(conversation, request.LifecycleRevision); err != nil {
			return nil, err
		}
	}
	pending := make([]struct {
		message   MessageInput
		storageID string
		key       string
	}, 0, len(normalized))
	for _, message := range normalized {
		key := messageKey(request.ConversationID, message.MessageID)
		if existing, ok := next.messages[key]; ok {
			if existing.contentSHA256 != message.ContentSHA256 {
				return nil, fmt.Errorf("%w: message %q already indexed with different content", ErrConflict, message.MessageID)
			}
			continue
		}
		storage := storageID(s.storageDomain, request.ConversationID, message.MessageID, request.OperationID)
		if intent, ok := next.pendingWrites[storage]; ok {
			if intent.OperationID != request.OperationID || intent.Fingerprint != message.ContentSHA256 {
				return nil, fmt.Errorf("%w: message %q is bound to another unfinished index operation", ErrConflict, message.MessageID)
			}
		} else {
			// The same storage id can still be waiting for physical deletion: an
			// earlier attempt wrote the intent, failed, and its cleanup could not
			// reach the collection. Claiming the id again supersedes that cleanup,
			// because the retry replaces the very vectors the delete was aiming at
			// (ingesting a storage id replaces all of its chunks). If this attempt
			// fails too, abandonWrite puts the id back on the delete list, so the
			// orphan is never forgotten. Keeping both claims would make the state
			// invalid - the validator refuses an id that is pending write and
			// delete - and the retry the contract prescribes would be rejected
			// with a message about a control state bug instead of making progress.
			delete(next.pendingDeletes, storage)
			next.pendingWrites[storage] = ControlPendingWrite{
				OperationID:             request.OperationID,
				Fingerprint:             message.ContentSHA256,
				OperationFingerprint:    fingerprint,
				LeaseExpiresAtUnixMilli: now().Add(pendingWriteLease).UnixMilli(),
			}
		}
		pending = append(pending, struct {
			message   MessageInput
			storageID string
			key       string
		}{message: message, storageID: storage, key: key})
	}
	if len(pending) > 0 {
		if err := s.commit(ctx, snapshot, next); err != nil {
			return nil, err
		}
		snapshot = next
		next = snapshot.clone()
	}

	// Phase two: write the vectors. Each message is independent, so one failure
	// only abandons that message's intent.
	chunkCounts := make(map[string]int, len(pending))
	var indexErr error
	for _, item := range pending {
		count, err := s.indexer.IndexMessage(ctx, item.storageID, item.message)
		if err != nil {
			abandoned, abandonErr := s.abandonWrite(ctx, snapshot, item.storageID)
			if abandonErr != nil {
				return nil, errors.Join(err, abandonErr)
			}
			snapshot = abandoned
			indexErr = errors.Join(indexErr, err)
			continue
		}
		chunkCounts[item.storageID] = count
	}
	if indexErr != nil {
		return nil, indexErr
	}

	// Phase three: publish the indexed state.
	next = snapshot.clone()
	conversation = next.conversation(request.ConversationID)
	if conversation.ownerScopeID == "" {
		conversation.ownerScopeID = request.OwnerScopeID
	}
	advanceLifecycle(conversation, request.LifecycleRevision)
	states := make([]MessageState, 0, len(normalized))
	for _, message := range normalized {
		key := messageKey(request.ConversationID, message.MessageID)
		existing, ok := next.messages[key]
		if !ok {
			storage := storageID(s.storageDomain, request.ConversationID, message.MessageID, request.OperationID)
			existing = &messageState{
				conversationID:    request.ConversationID,
				messageID:         message.MessageID,
				senderID:          message.SenderID,
				sentAtUnixMs:      message.SentAtUnixMs,
				contentSHA256:     message.ContentSHA256,
				storageID:         storage,
				chunkCount:        chunkCounts[storage],
				lifecycleRevision: request.LifecycleRevision,
				metadata:          cloneStringMap(message.Metadata),
			}
			next.messages[key] = existing
			next.messagesByStorage[storage] = key
			delete(next.pendingWrites, storage)
		}
		conversation := next.conversations[request.ConversationID]
		states = append(states, messageStateOf(existing, conversation))
	}
	next.recordOperation(request.OperationID, operationIndexMessages, fingerprint, states)
	if err := s.commit(ctx, snapshot, next); err != nil {
		return nil, err
	}
	return states, nil
}

// ArchiveConversation marks a conversation as retrievable. Indexing alone never
// makes a message searchable.
func (s *IndexService) ArchiveConversation(ctx context.Context, request ArchiveConversationRequest) (ArchiveResult, error) {
	request.OperationID = strings.TrimSpace(request.OperationID)
	request.ConversationID = strings.TrimSpace(request.ConversationID)
	if request.OperationID == "" || request.ConversationID == "" || request.ArchiveRevision == 0 {
		return ArchiveResult{}, fmt.Errorf("%w: operation_id, conversation_id and archive_revision are required", ErrInvalidInput)
	}
	fingerprint := operationFingerprint(struct {
		ConversationID    string
		ArchiveRevision   uint64
		LifecycleRevision uint64
	}{request.ConversationID, request.ArchiveRevision, request.LifecycleRevision})

	locked, err := s.lockWrite(ctx)
	if err != nil {
		return ArchiveResult{}, err
	}
	defer s.unlockWrite()
	snapshot := locked
	next := snapshot.clone()

	if replay, ok, err := next.replayOperation(request.OperationID, operationArchive, fingerprint); err != nil {
		return ArchiveResult{}, err
	} else if ok {
		return replay.(ArchiveResult), nil
	}
	// A replay above is answered from the ledger; anything that reaches this point
	// would grow the snapshot, so the ceiling applies to it too.
	if err := s.checkSnapshotBudget(); err != nil {
		return ArchiveResult{}, err
	}
	conversation := next.lookup(request.ConversationID)
	if conversation == nil || conversation.ownerScopeID == "" {
		return ArchiveResult{}, fmt.Errorf("%w: conversation", ErrNotFound)
	}
	if request.ArchiveRevision < conversation.archiveRevision {
		return ArchiveResult{}, fmt.Errorf("%w: current=%d requested=%d", ErrStaleArchive, conversation.archiveRevision, request.ArchiveRevision)
	}
	if err := validateLifecycle(conversation, request.LifecycleRevision); err != nil {
		return ArchiveResult{}, err
	}
	conversation.archived = true
	conversation.archiveRevision = request.ArchiveRevision
	conversation.tombstoned = false
	advanceLifecycle(conversation, request.LifecycleRevision)
	result := ArchiveResult{
		Status:            StatusArchived,
		ArchiveRevision:   conversation.archiveRevision,
		LifecycleRevision: conversation.lifecycleRevision,
	}
	next.recordOperation(request.OperationID, operationArchive, fingerprint, result)
	if err := s.commit(ctx, snapshot, next); err != nil {
		return ArchiveResult{}, err
	}
	return result, nil
}

// UpdateConversationAccess replaces the complete access snapshot. Chat has no
// public equivalent, so an empty snapshot grants nothing.
func (s *IndexService) UpdateConversationAccess(ctx context.Context, request UpdateConversationAccessRequest) (AccessState, error) {
	request.OperationID = strings.TrimSpace(request.OperationID)
	request.ConversationID = strings.TrimSpace(request.ConversationID)
	request.GrantedScopeIDs = normalizeIDs(request.GrantedScopeIDs)
	if request.OperationID == "" || request.ConversationID == "" || request.AccessRevision == 0 {
		return AccessState{}, fmt.Errorf("%w: operation_id, conversation_id and access_revision are required", ErrInvalidInput)
	}
	fingerprint := operationFingerprint(struct {
		ConversationID    string
		AccessRevision    uint64
		LifecycleRevision uint64
		GrantedScopeIDs   []string
	}{request.ConversationID, request.AccessRevision, request.LifecycleRevision, request.GrantedScopeIDs})

	locked, err := s.lockWrite(ctx)
	if err != nil {
		return AccessState{}, err
	}
	defer s.unlockWrite()
	snapshot := locked
	next := snapshot.clone()

	if replay, ok, err := next.replayOperation(request.OperationID, operationUpdateAccess, fingerprint); err != nil {
		return AccessState{}, err
	} else if ok {
		return cloneAccessState(replay.(AccessState)), nil
	}
	if err := s.checkSnapshotBudget(); err != nil {
		return AccessState{}, err
	}
	conversation := next.lookup(request.ConversationID)
	if conversation == nil || conversation.ownerScopeID == "" {
		return AccessState{}, fmt.Errorf("%w: conversation", ErrNotFound)
	}
	if request.AccessRevision < conversation.accessRevision {
		return AccessState{}, fmt.Errorf("%w: current=%d requested=%d", ErrStaleAccess, conversation.accessRevision, request.AccessRevision)
	}
	if request.AccessRevision == conversation.accessRevision &&
		!equalStrings(request.GrantedScopeIDs, conversation.grantedScopeIDs) {
		return AccessState{}, fmt.Errorf("%w: access revision %d has a different scope snapshot", ErrConflict, conversation.accessRevision)
	}
	if err := validateLifecycle(conversation, request.LifecycleRevision); err != nil {
		return AccessState{}, err
	}
	conversation.accessRevision = request.AccessRevision
	conversation.grantedScopeIDs = cloneStrings(request.GrantedScopeIDs)
	advanceLifecycle(conversation, request.LifecycleRevision)
	state := accessStateOf(request.ConversationID, conversation)
	next.recordOperation(request.OperationID, operationUpdateAccess, fingerprint, state)
	if err := s.commit(ctx, snapshot, next); err != nil {
		return AccessState{}, err
	}
	return state, nil
}

// RetractMessage removes one message from retrieval. The message stays indexed
// and stored: retraction is a retrievability decision, not a deletion.
func (s *IndexService) RetractMessage(ctx context.Context, request RetractMessageRequest) (RetractResult, error) {
	request.OperationID = strings.TrimSpace(request.OperationID)
	request.ConversationID = strings.TrimSpace(request.ConversationID)
	request.MessageID = strings.TrimSpace(request.MessageID)
	if request.OperationID == "" || request.ConversationID == "" || request.MessageID == "" || request.RetractRevision == 0 {
		return RetractResult{}, fmt.Errorf("%w: operation_id, conversation_id, message_id and retract_revision are required", ErrInvalidInput)
	}
	fingerprint := operationFingerprint(struct {
		ConversationID    string
		MessageID         string
		RetractRevision   uint64
		LifecycleRevision uint64
	}{request.ConversationID, request.MessageID, request.RetractRevision, request.LifecycleRevision})

	locked, err := s.lockWrite(ctx)
	if err != nil {
		return RetractResult{}, err
	}
	defer s.unlockWrite()
	snapshot := locked
	next := snapshot.clone()

	if replay, ok, err := next.replayOperation(request.OperationID, operationRetract, fingerprint); err != nil {
		return RetractResult{}, err
	} else if ok {
		return replay.(RetractResult), nil
	}
	if err := s.checkSnapshotBudget(); err != nil {
		return RetractResult{}, err
	}
	conversation := next.conversations[request.ConversationID]
	if conversation == nil {
		return RetractResult{}, fmt.Errorf("%w: conversation", ErrNotFound)
	}
	message, ok := next.messages[messageKey(request.ConversationID, request.MessageID)]
	if !ok {
		return RetractResult{}, fmt.Errorf("%w: message", ErrNotFound)
	}
	if request.RetractRevision < message.retractRevision {
		return RetractResult{}, fmt.Errorf("%w: current=%d requested=%d", ErrStaleRetract, message.retractRevision, request.RetractRevision)
	}
	if err := validateLifecycle(conversation, request.LifecycleRevision); err != nil {
		return RetractResult{}, err
	}
	message.retracted = true
	message.retractRevision = request.RetractRevision
	advanceLifecycle(conversation, request.LifecycleRevision)
	result := RetractResult{Retracted: true, RetractRevision: message.retractRevision}
	next.recordOperation(request.OperationID, operationRetract, fingerprint, result)
	if err := s.commit(ctx, snapshot, next); err != nil {
		return RetractResult{}, err
	}
	return result, nil
}

// DeleteConversation tombstones a conversation and removes its derived index.
// The tombstone and the decision revisions stay, so a late event can neither
// resurrect the conversation nor replay an old deletion.
func (s *IndexService) DeleteConversation(ctx context.Context, request DeleteConversationRequest) (DeleteResult, error) {
	request.OperationID = strings.TrimSpace(request.OperationID)
	request.ConversationID = strings.TrimSpace(request.ConversationID)
	if request.OperationID == "" || request.ConversationID == "" || request.LifecycleRevision == 0 {
		return DeleteResult{}, fmt.Errorf("%w: operation_id, conversation_id and lifecycle_revision are required", ErrInvalidInput)
	}
	fingerprint := operationFingerprint(struct {
		ConversationID    string
		LifecycleRevision uint64
	}{request.ConversationID, request.LifecycleRevision})

	locked, err := s.lockWrite(ctx)
	if err != nil {
		return DeleteResult{}, err
	}
	defer s.unlockWrite()
	snapshot := locked
	next := snapshot.clone()

	if replay, ok, err := next.replayOperation(request.OperationID, operationDelete, fingerprint); err != nil {
		return DeleteResult{}, err
	} else if ok {
		return replay.(DeleteResult), nil
	}
	// A delete shrinks the indexed messages but still records a ledger entry, so it
	// passes the same ceiling as every other mutation.
	if err := s.checkSnapshotBudget(); err != nil {
		return DeleteResult{}, err
	}
	conversation := next.conversation(request.ConversationID)
	// The staleness fence runs before the cached result on purpose. A cached
	// result describes the state at that lifecycle revision; once a later
	// revision resurrected the conversation, replaying the old revision would
	// answer "tombstoned" for a conversation that is live and searchable again.
	// A replay of the same revision is still idempotent: it is at the current
	// lifecycle revision, so it passes the fence and hits the cache below.
	if request.LifecycleRevision < conversation.lifecycleRevision {
		return DeleteResult{}, fmt.Errorf("%w: current=%d requested=%d", ErrStaleLifecycle, conversation.lifecycleRevision, request.LifecycleRevision)
	}
	if result, ok := conversation.deleteResults[request.LifecycleRevision]; ok {
		next.recordOperation(request.OperationID, operationDelete, fingerprint, result)
		if err := s.commit(ctx, snapshot, next); err != nil {
			return DeleteResult{}, err
		}
		return result, nil
	}
	if request.LifecycleRevision == conversation.lifecycleRevision && conversation.ownerScopeID != "" {
		return DeleteResult{}, fmt.Errorf("%w: lifecycle revision %d already represents a live conversation", ErrConflict, request.LifecycleRevision)
	}

	for key, message := range next.messages {
		if message.conversationID != request.ConversationID {
			continue
		}
		delete(next.messagesByStorage, message.storageID)
		delete(next.messages, key)
		next.pendingDeletes[message.storageID] = struct{}{}
	}
	conversation.archived = false
	conversation.lifecycleRevision = request.LifecycleRevision
	conversation.tombstoneRevision = request.LifecycleRevision
	conversation.tombstoned = true
	result := DeleteResult{Tombstoned: true, LifecycleRevision: request.LifecycleRevision}
	conversation.deleteResults[request.LifecycleRevision] = result
	next.recordOperation(request.OperationID, operationDelete, fingerprint, result)
	if err := s.commit(ctx, snapshot, next); err != nil {
		return DeleteResult{}, err
	}
	if err := s.cleanupPendingDeletes(ctx, next); err != nil {
		return DeleteResult{}, err
	}
	return result, nil
}

// GetConversationIndexState is the reconciliation view: go-web and py-agent use
// it to compare their own records with what this corpus actually indexed.
func (s *IndexService) GetConversationIndexState(ctx context.Context, request GetConversationIndexStateRequest) (ConversationIndexState, error) {
	request.ConversationID = strings.TrimSpace(request.ConversationID)
	if request.ConversationID == "" {
		return ConversationIndexState{}, fmt.Errorf("%w: conversation_id is required", ErrInvalidInput)
	}
	snapshot, err := s.readSnapshot(ctx)
	if err != nil {
		return ConversationIndexState{}, err
	}
	conversation := snapshot.conversations[request.ConversationID]
	if conversation == nil {
		return ConversationIndexState{}, nil
	}
	status := StatusIndexed
	if conversation.archived && !conversation.tombstoned {
		status = StatusArchived
	}
	state := ConversationIndexState{
		Exists:            true,
		Status:            status,
		OwnerScopeID:      conversation.ownerScopeID,
		ArchiveRevision:   conversation.archiveRevision,
		AccessRevision:    conversation.accessRevision,
		LifecycleRevision: conversation.lifecycleRevision,
		TombstoneRevision: conversation.tombstoneRevision,
		Tombstoned:        conversation.tombstoned,
	}
	for _, message := range snapshot.messages {
		if message.conversationID != request.ConversationID {
			continue
		}
		state.IndexedMessageCount++
		if message.retracted {
			state.RetractedMessageCount++
		}
	}
	return state, nil
}

// SearchMessages recalls chat candidates and applies the retrievability rules.
//
// A conversation must be archived and not tombstoned, the message must not be
// retracted, and the request must name an authorized scope or conversation. Chat
// has no public corpus, so two empty allow-lists return nothing instead of
// everything.
func (s *IndexService) SearchMessages(ctx context.Context, request SearchMessagesRequest) (SearchMessagesResult, error) {
	request.Query = strings.TrimSpace(request.Query)
	if request.Query == "" {
		return SearchMessagesResult{}, fmt.Errorf("%w: query is required", ErrInvalidInput)
	}
	if request.TopK < 0 || request.TopK > 100 {
		return SearchMessagesResult{}, fmt.Errorf("%w: top_k must be between 0 and 100", ErrInvalidInput)
	}
	if request.TopK == 0 {
		request.TopK = 3
	}
	allowedScopes := stringSet(request.AllowedScopeIDs)
	allowedConversations := stringSet(request.AllowedConversationIDs)
	if len(allowedScopes) == 0 && len(allowedConversations) == 0 {
		return SearchMessagesResult{Query: request.Query}, nil
	}
	if s.searcher == nil {
		return SearchMessagesResult{}, fmt.Errorf("%w: chat retrieval is not configured", ErrProjectionUnavailable)
	}

	snapshot, err := s.readSnapshot(ctx)
	if err != nil {
		return SearchMessagesResult{}, err
	}
	if err := s.ensureProjection(ctx, snapshot); err != nil {
		return SearchMessagesResult{}, err
	}

	candidateLimit := max(request.TopK*2, 16)
	maxCandidateLimit := min(max(request.TopK*16, 128), 800)
	for {
		raw, err := s.searcher.SearchMessages(ctx, request.Query, candidateLimit)
		if err != nil {
			return SearchMessagesResult{}, err
		}
		hits := make([]MessageHit, 0, request.TopK)
		seen := make(map[string]struct{}, request.TopK)
		for _, candidate := range raw {
			message, ok := s.authorizedMessage(snapshot, candidate, allowedScopes, allowedConversations, request)
			if !ok {
				continue
			}
			if _, duplicate := seen[candidate.StorageID]; duplicate {
				continue
			}
			seen[candidate.StorageID] = struct{}{}
			conversation := snapshot.conversations[message.conversationID]
			hits = append(hits, MessageHit{
				ConversationID: message.conversationID,
				MessageID:      message.messageID,
				OwnerScopeID:   conversation.ownerScopeID,
				SenderID:       message.senderID,
				SentAtUnixMs:   message.sentAtUnixMs,
				Position:       candidate.Position,
				Snippet:        candidate.Snippet,
				ContentSHA256:  message.contentSHA256,
				Score:          candidate.Score,
				DenseRank:      candidate.DenseRank,
				SparseRank:     candidate.SparseRank,
			})
			if len(hits) == request.TopK {
				break
			}
		}
		exhausted := len(raw) < candidateLimit
		if len(hits) == request.TopK || exhausted || candidateLimit >= maxCandidateLimit {
			return SearchMessagesResult{
				Query:     request.Query,
				Hits:      hits,
				Truncated: len(hits) < request.TopK && !exhausted,
			}, nil
		}
		candidateLimit = min(candidateLimit*2, maxCandidateLimit)
	}
}

// authorizedMessage is the single place that decides whether a candidate may be
// returned. The vector store filters too, but a store that over-returns must not
// be able to leak, so the decision is repeated here against the snapshot the
// request was admitted with.
func (s *IndexService) authorizedMessage(
	snapshot *snapshot,
	candidate ScoredMessageChunk,
	allowedScopes map[string]struct{},
	allowedConversations map[string]struct{},
	request SearchMessagesRequest,
) (*messageState, bool) {
	key, ok := snapshot.messagesByStorage[candidate.StorageID]
	if !ok {
		return nil, false
	}
	message, ok := snapshot.messages[key]
	if !ok {
		return nil, false
	}
	conversation := snapshot.conversations[message.conversationID]
	if conversation == nil || !conversation.archived || conversation.tombstoned {
		return nil, false
	}
	if message.retracted {
		return nil, false
	}
	if request.SentAfterUnixMs > 0 && message.sentAtUnixMs < request.SentAfterUnixMs {
		return nil, false
	}
	if request.SentBeforeUnixMs > 0 && message.sentAtUnixMs > request.SentBeforeUnixMs {
		return nil, false
	}
	if _, ok := allowedConversations[message.conversationID]; ok {
		return message, true
	}
	if _, ok := allowedScopes[conversation.ownerScopeID]; ok {
		return message, true
	}
	for _, granted := range conversation.grantedScopeIDs {
		if _, ok := allowedScopes[granted]; ok {
			return message, true
		}
	}
	return nil, false
}

// readSnapshot returns the snapshot a read-only call should serve. The steady
// state costs one generation probe and no lock; a changed generation reloads
// under reloadMu, and pending maintenance is opportunistic so a reader never
// queues behind a writer.
func (s *IndexService) readSnapshot(ctx context.Context) (*snapshot, error) {
	current := s.state.Load()
	changed, err := s.controlStoreChanged(ctx, current)
	if err != nil {
		return nil, err
	}
	if changed {
		s.reloadMu.Lock()
		defer s.reloadMu.Unlock()
		return s.reload(ctx, s.state.Load())
	}
	if !current.needsMaintenance(now().UnixMilli()) {
		return current, nil
	}
	if !s.writeMu.TryLock() {
		return current, nil
	}
	defer s.writeMu.Unlock()
	s.reloadMu.Lock()
	defer s.reloadMu.Unlock()
	return s.reload(ctx, s.state.Load())
}

func (s *IndexService) controlStoreChanged(ctx context.Context, current *snapshot) (bool, error) {
	generation, err := s.store.Generation(ctx)
	if err != nil {
		return false, fmt.Errorf("%w: read chat control generation: %w", ErrControlStoreUnavailable, err)
	}
	if generation < current.generation {
		return false, fmt.Errorf(
			"%w: chat control generation regressed from %d to %d",
			ErrControlStoreUnavailable, current.generation, generation,
		)
	}
	return generation != current.generation, nil
}

// lockWrite serializes control-plane mutations and returns the snapshot they
// build on.
func (s *IndexService) lockWrite(ctx context.Context) (*snapshot, error) {
	s.writeMu.Lock()
	s.reloadMu.Lock()
	defer s.reloadMu.Unlock()
	current, err := s.reload(ctx, s.state.Load())
	if err != nil {
		s.writeMu.Unlock()
		return nil, err
	}
	return current, nil
}

func (s *IndexService) unlockWrite() { s.writeMu.Unlock() }

// reload loads the durable chat control plane, fails closed on regression,
// finishes pending vector maintenance, and publishes the result.
func (s *IndexService) reload(ctx context.Context, current *snapshot) (*snapshot, error) {
	state, err := s.store.Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: load chat control state: %w", ErrControlStoreUnavailable, err)
	}
	if state.Generation < current.generation {
		return nil, fmt.Errorf(
			"%w: chat control generation regressed from %d to %d",
			ErrControlStoreUnavailable, current.generation, state.Generation,
		)
	}
	loaded, err := snapshotFromControlState(state)
	if err != nil {
		return nil, fmt.Errorf("%w: load chat control state: %w", ErrControlStoreUnavailable, err)
	}
	s.publish(loaded)
	// The snapshot just read is the one the guard must compare against: another
	// instance may have grown it since this one last wrote.
	s.refreshSnapshotBytes()
	if err := s.fenceExpiredWrites(ctx, loaded); err != nil {
		return nil, err
	}
	if err := s.cleanupPendingDeletes(ctx, s.state.Load()); err != nil {
		return nil, err
	}
	// Retention runs on this same opportunistic pass (ADR-015): the ledger is the
	// one part of the snapshot with no other reclamation path.
	//
	// This pass can also run from a read path, which holds reloadMu but not the
	// writer lock, so the prune's compare-and-swap can lose to a concurrent writer.
	// Losing is harmless - the winner's snapshot is newer and the next pass will
	// see the same expired entries - so a conflict is swallowed rather than turned
	// into a failed read. That is why this comment does not claim the writer lock.
	if err := s.pruneOperationLedger(ctx, s.state.Load()); err != nil {
		return nil, err
	}
	return s.state.Load(), nil
}

// pruneOperationLedger drops ledger entries outside the retention window, then
// whatever the count ceiling still requires, in one compare-and-swap commit.
//
// Nothing happens when retention is disabled or nothing is eligible, so the pass
// costs one scan of the ledger and no write. Entries without a timestamp are kept
// by age and dropped first by the ceiling (ADR-015).
func (s *IndexService) pruneOperationLedger(ctx context.Context, current *snapshot) error {
	if s.operationRetention <= 0 && s.maxOperationEntries <= 0 {
		return nil
	}
	victims := current.pruneOperationLedger(now().UnixMilli(), s.operationRetention.Milliseconds(), s.maxOperationEntries)
	if len(victims) == 0 {
		return nil
	}
	next := current.clone()
	for _, operationID := range victims {
		delete(next.operations, operationID)
	}
	if err := s.commit(ctx, current, next); err != nil {
		// Opportunistic maintenance may run from a read path, where a concurrent
		// writer can win the compare-and-swap. The winner persisted a newer
		// snapshot and the next pass will prune the same entries, so a lost race is
		// not a failure of the request that happened to trigger the pass.
		if errors.Is(err, ErrControlStoreConflict) {
			return nil
		}
		return fmt.Errorf("prune chat operation ledger: %w", err)
	}
	return nil
}

// commit persists a writer-private snapshot with compare-and-swap and publishes
// it only after the durable write succeeded.
func (s *IndexService) commit(ctx context.Context, previous, next *snapshot) error {
	next.generation = previous.generation
	generation, err := s.store.Save(ctx, previous.generation, next.toControlState())
	if err != nil {
		if errors.Is(err, ErrControlStoreConflict) {
			return fmt.Errorf("%w: persist chat control state: %v", ErrControlStoreConflict, err)
		}
		return fmt.Errorf("%w: persist chat control state: %w", ErrControlStoreUnavailable, err)
	}
	next.generation = generation
	s.refreshSnapshotBytes()
	s.publish(next)
	return nil
}

func (s *IndexService) publish(next *snapshot) {
	if s.state.Publish(next, next.generation) {
		s.projection.Signal()
	}
}

// abandonWrite fences one failed vector write into a pending delete and cleans
// the orphaned vectors.
func (s *IndexService) abandonWrite(ctx context.Context, current *snapshot, storage string) (*snapshot, error) {
	next := current.clone()
	delete(next.pendingWrites, storage)
	next.pendingDeletes[storage] = struct{}{}
	if err := s.commit(ctx, current, next); err != nil {
		return nil, fmt.Errorf("claim abandoned chat vector write: %w", err)
	}
	if err := s.cleanupPendingDeletes(ctx, next); err != nil {
		return nil, fmt.Errorf("clean abandoned chat vector write: %w", err)
	}
	return s.state.Load(), nil
}

// fenceExpiredWrites turns an expired write intent into a durable pending delete
// before anything physical happens, so a crashed index call cannot leave vectors
// that no intent accounts for.
func (s *IndexService) fenceExpiredWrites(ctx context.Context, current *snapshot) error {
	if len(current.pendingWrites) == 0 {
		return nil
	}
	nowMilli := now().UnixMilli()
	expired := false
	for _, intent := range current.pendingWrites {
		if intent.LeaseExpiresAtUnixMilli <= nowMilli {
			expired = true
			break
		}
	}
	if !expired {
		return nil
	}
	next := current.clone()
	for storage, intent := range next.pendingWrites {
		if intent.LeaseExpiresAtUnixMilli > nowMilli {
			continue
		}
		delete(next.pendingWrites, storage)
		next.pendingDeletes[storage] = struct{}{}
	}
	if err := s.commit(ctx, current, next); err != nil {
		return fmt.Errorf("fence expired chat vector write: %w", err)
	}
	return nil
}

// cleanupPendingDeletes performs only physical cleanup: the logical decision is
// already durable, so a store failure leaves an invisible, retryable orphan.
func (s *IndexService) cleanupPendingDeletes(ctx context.Context, current *snapshot) error {
	if len(current.pendingDeletes) == 0 {
		return nil
	}
	next := current.clone()
	removed := false
	for storage := range next.pendingDeletes {
		if err := s.indexer.DeleteMessage(ctx, storage); err != nil {
			continue
		}
		delete(next.pendingDeletes, storage)
		removed = true
	}
	if !removed {
		return nil
	}
	if err := s.commit(ctx, current, next); err != nil {
		return fmt.Errorf("persist chat vector cleanup progress: %w", err)
	}
	return nil
}

// ensureProjection guarantees the derived index is at least as new as the
// snapshot a search is about to filter against, and fails closed when it cannot
// be brought there.
func (s *IndexService) ensureProjection(ctx context.Context, current *snapshot) error {
	if s.projectionStore == nil {
		return nil
	}
	if s.projection.Synced() >= current.generation {
		return nil
	}
	return s.projection.Converge(ctx, current.generation, func(inner context.Context) error {
		return s.syncProjection(inner, current)
	})
}

func (s *IndexService) syncProjection(ctx context.Context, current *snapshot) error {
	if err := s.projectionStore.SyncChatControls(ctx, current.vectorControls(s.storageDomain)); err != nil {
		return fmt.Errorf("%w: %w", ErrProjectionUnavailable, err)
	}
	return nil
}

// checkSnapshotBudget refuses a mutation once the persisted snapshot is at or
// above the configured ceiling.
//
// Every mutating path calls this, not only indexing: archive, access, retraction
// and deletion each add a ledger entry, so a limit enforced on indexing alone
// would not bound the snapshot at all. The rule an operator gets is therefore
// simple and worth stating: at the ceiling the corpus refuses writes and keeps
// serving reads.
func (s *IndexService) checkSnapshotBudget() error {
	if s.maxSnapshotBytes <= 0 {
		return nil
	}
	if size := s.lastSnapshotBytes.Load(); size >= s.maxSnapshotBytes {
		return fmt.Errorf(
			"%w: the persisted control snapshot is %d bytes, at or above the limit of %d",
			ErrCapacityExceeded, size, s.maxSnapshotBytes,
		)
	}
	return nil
}

// checkCapacity refuses a batch that would push this corpus past a configured
// hard limit.
//
// The message limit is exact: it counts the messages the batch would add to the
// snapshot. The snapshot limit is checked against the persisted snapshot rather
// than an encoding of the candidate state, which would roughly double the cost of
// every write; enforcement can therefore lag by one write, and the error says
// which limit was hit so an operator can act on it.
func (s *IndexService) checkCapacity(current *snapshot, conversationID string, batch []MessageInput) error {
	if s.maxMessages > 0 {
		additional := 0
		for _, message := range batch {
			if _, indexed := current.messages[messageKey(conversationID, message.MessageID)]; indexed {
				continue
			}
			additional++
		}
		if len(current.messages)+additional > s.maxMessages {
			return fmt.Errorf(
				"%w: %d indexed messages plus %d new would exceed the limit of %d",
				ErrCapacityExceeded, len(current.messages), additional, s.maxMessages,
			)
		}
	}
	return s.checkSnapshotBudget()
}

func validateLifecycle(conversation *conversationState, revision uint64) error {
	if conversation.tombstoned {
		if revision <= conversation.tombstoneRevision {
			return fmt.Errorf("%w: conversation is tombstoned at %d", ErrStaleLifecycle, conversation.tombstoneRevision)
		}
		return nil
	}
	if revision < conversation.lifecycleRevision {
		return fmt.Errorf("%w: current=%d requested=%d", ErrStaleLifecycle, conversation.lifecycleRevision, revision)
	}
	return nil
}

func advanceLifecycle(conversation *conversationState, revision uint64) {
	if revision > conversation.lifecycleRevision {
		conversation.lifecycleRevision = revision
	}
}

func messageStateOf(message *messageState, conversation *conversationState) MessageState {
	state := MessageState{
		ConversationID:    message.conversationID,
		MessageID:         message.messageID,
		OwnerScopeID:      conversation.ownerScopeID,
		SenderID:          message.senderID,
		SentAtUnixMs:      message.sentAtUnixMs,
		LifecycleRevision: message.lifecycleRevision,
		Retracted:         message.retracted,
		RetractRevision:   message.retractRevision,
		ChunkCount:        message.chunkCount,
		ContentSHA256:     message.contentSHA256,
	}
	state.ArchiveRevision = conversation.archiveRevision
	state.AccessRevision = conversation.accessRevision
	return state
}

func accessStateOf(conversationID string, conversation *conversationState) AccessState {
	return AccessState{
		ConversationID:    conversationID,
		AccessRevision:    conversation.accessRevision,
		LifecycleRevision: conversation.lifecycleRevision,
		GrantedScopeIDs:   cloneStrings(conversation.grantedScopeIDs),
	}
}

func stringSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range normalizeIDs(values) {
		set[value] = struct{}{}
	}
	return set
}
