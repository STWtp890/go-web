package rag

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const pendingVectorWriteLease = 15 * time.Minute

var (
	// ErrControlStoreConflict reports that another service instance committed a
	// newer control-state generation. P2.1a detects this condition but does not
	// retry vector-store side effects across service instances automatically.
	ErrControlStoreConflict = errors.New("control store generation conflict")
	// ErrControlStoreUnavailable marks a service instance as failed closed after
	// it can no longer prove that its in-memory state matches durable state.
	ErrControlStoreUnavailable = errors.New("control store unavailable")
	// ErrControlStoreUninitialized prevents a missing namespace from being
	// mistaken for an empty, authoritative control plane after data loss.
	ErrControlStoreUninitialized = errors.New("control store namespace is not initialized")
)

const (
	operationIndexDocumentVersion    = "index_document_version"
	operationActivateDocumentVersion = "activate_document_version"
	operationUpdateDocumentAccess    = "update_document_access"
	operationDeleteDocumentVersion   = "delete_document_version"
	operationDeleteDocument          = "delete_document"
)

// ControlStore persists the complete contract control plane. Save implements
// compare-and-swap: it may commit only when expectedGeneration is current and
// returns the newly committed generation.
//
// Generation is the cheap staleness probe a reader uses to decide whether the
// published snapshot is still current. It must observe the same generation Save
// returns, without loading the control plane.
type ControlStore interface {
	Load(context.Context) (ControlState, error)
	Generation(context.Context) (uint64, error)
	Save(context.Context, uint64, ControlState) (uint64, error)
	StorageDomain() string
}

// ControlState is a transport-independent persistence model. Protobuf messages
// never enter this type. VersionsByStore is stored explicitly so a corrupt or
// incomplete snapshot can fail closed during startup instead of being guessed.
type ControlState struct {
	Generation           uint64                               `json:"-"`
	VersionsByKey        map[string]ControlVersion            `json:"versions_by_key"`
	VersionsByStore      map[string]string                    `json:"versions_by_store"`
	VersionFingerprints  map[string]ControlVersionFingerprint `json:"version_fingerprints"`
	Manifests            map[string]ControlDocumentManifest   `json:"manifests"`
	Operations           map[string]ControlOperation          `json:"operations"`
	PendingVectorWrites  map[string]ControlPendingVectorWrite `json:"pending_vector_writes"`
	PendingVectorDeletes map[string]bool                      `json:"pending_vector_deletes"`
}

type ControlPendingVectorWrite struct {
	OperationID             string `json:"operation_id"`
	Fingerprint             string `json:"fingerprint"`
	LeaseExpiresAtUnixMilli int64  `json:"lease_expires_at_unix_milli"`
}

type ControlVersion struct {
	State     DocumentVersionState `json:"state"`
	StorageID string               `json:"storage_id"`
	SourceURI string               `json:"source_uri"`
	Metadata  map[string]string    `json:"metadata"`
}

type ControlVersionFingerprint struct {
	ContentSHA256 string `json:"content_sha256"`
	OwnerSpaceID  string `json:"owner_space_id"`
}

type ControlDocumentManifest struct {
	OwnerSpaceID          string                                            `json:"owner_space_id"`
	ActiveVersionID       string                                            `json:"active_version_id"`
	ActivationVersionID   string                                            `json:"activation_version_id"`
	ActivationRevision    uint64                                            `json:"activation_revision"`
	ActivationState       DocumentVersionState                              `json:"activation_state"`
	AccessRevision        uint64                                            `json:"access_revision"`
	AuthenticatedPublic   bool                                              `json:"authenticated_public"`
	GrantedSpaceIDs       []string                                          `json:"granted_space_ids"`
	LifecycleRevision     uint64                                            `json:"lifecycle_revision"`
	TombstoneRevision     uint64                                            `json:"tombstone_revision"`
	Tombstoned            bool                                              `json:"tombstoned"`
	DocumentDeleteResults map[uint64]DeleteDocumentResult                   `json:"document_delete_results"`
	VersionDeleteResults  map[string]map[uint64]DeleteDocumentVersionResult `json:"version_delete_results"`
}

// ControlOperationResult is a typed union. Exactly one field must be present
// and it must match ControlOperation.Kind, preserving the first RPC response
// across process restarts without persisting protocol DTOs.
type ControlOperationResult struct {
	DocumentVersion       *DocumentVersionState        `json:"document_version,omitempty"`
	DocumentAccess        *DocumentAccessState         `json:"document_access,omitempty"`
	DeleteDocumentVersion *DeleteDocumentVersionResult `json:"delete_document_version,omitempty"`
	DeleteDocument        *DeleteDocumentResult        `json:"delete_document,omitempty"`
}

type ControlOperation struct {
	Kind        string                 `json:"kind"`
	Fingerprint string                 `json:"fingerprint"`
	Result      ControlOperationResult `json:"result"`
}

func newControlState() ControlState {
	return ControlState{
		VersionsByKey:        make(map[string]ControlVersion),
		VersionsByStore:      make(map[string]string),
		VersionFingerprints:  make(map[string]ControlVersionFingerprint),
		Manifests:            make(map[string]ControlDocumentManifest),
		Operations:           make(map[string]ControlOperation),
		PendingVectorWrites:  make(map[string]ControlPendingVectorWrite),
		PendingVectorDeletes: make(map[string]bool),
	}
}

func cloneControlState(state ControlState) (ControlState, error) {
	payload, err := json.Marshal(state)
	if err != nil {
		return ControlState{}, fmt.Errorf("marshal control state: %w", err)
	}
	var clone ControlState
	if err := json.Unmarshal(payload, &clone); err != nil {
		return ControlState{}, fmt.Errorf("unmarshal control state clone: %w", err)
	}
	clone.Generation = state.Generation
	normalizeControlState(&clone)
	if err := validateControlState(clone); err != nil {
		return ControlState{}, err
	}
	return clone, nil
}

func normalizeControlState(state *ControlState) {
	if state.VersionsByKey == nil {
		state.VersionsByKey = make(map[string]ControlVersion)
	}
	if state.VersionsByStore == nil {
		state.VersionsByStore = make(map[string]string)
	}
	if state.VersionFingerprints == nil {
		state.VersionFingerprints = make(map[string]ControlVersionFingerprint)
	}
	if state.Manifests == nil {
		state.Manifests = make(map[string]ControlDocumentManifest)
	}
	if state.Operations == nil {
		state.Operations = make(map[string]ControlOperation)
	}
	if state.PendingVectorWrites == nil {
		state.PendingVectorWrites = make(map[string]ControlPendingVectorWrite)
	}
	if state.PendingVectorDeletes == nil {
		state.PendingVectorDeletes = make(map[string]bool)
	}
	for documentID, manifest := range state.Manifests {
		if manifest.DocumentDeleteResults == nil {
			manifest.DocumentDeleteResults = make(map[uint64]DeleteDocumentResult)
		}
		if manifest.VersionDeleteResults == nil {
			manifest.VersionDeleteResults = make(map[string]map[uint64]DeleteDocumentVersionResult)
		}
		for versionID, results := range manifest.VersionDeleteResults {
			if results == nil {
				manifest.VersionDeleteResults[versionID] = make(map[uint64]DeleteDocumentVersionResult)
			}
		}
		state.Manifests[documentID] = manifest
	}
}

func validateControlState(state ControlState) error {
	for storageID, key := range state.VersionsByStore {
		version, ok := state.VersionsByKey[key]
		if !ok || version.StorageID != storageID {
			return fmt.Errorf("invalid control state: storage mapping %q references version %q", storageID, key)
		}
	}
	for key, version := range state.VersionsByKey {
		if version.State.DocumentID == "" || version.State.VersionID == "" || version.StorageID == "" {
			return fmt.Errorf("invalid control state: version %q is incomplete", key)
		}
		if key != documentVersionKey(version.State.DocumentID, version.State.VersionID) {
			return fmt.Errorf("invalid control state: version %q has a mismatched natural key", key)
		}
		if mapped, ok := state.VersionsByStore[version.StorageID]; !ok || mapped != key {
			return fmt.Errorf("invalid control state: version %q has no matching storage mapping", key)
		}
		fingerprint, ok := state.VersionFingerprints[key]
		if !ok || fingerprint.ContentSHA256 != version.State.ContentSHA256 ||
			fingerprint.OwnerSpaceID != version.State.OwnerSpaceID {
			return fmt.Errorf("invalid control state: version %q has no matching immutable fingerprint", key)
		}
		manifest, ok := state.Manifests[version.State.DocumentID]
		if !ok || manifest.OwnerSpaceID != version.State.OwnerSpaceID {
			return fmt.Errorf("invalid control state: version %q has no matching document manifest", key)
		}
	}
	for documentID, manifest := range state.Manifests {
		if documentID == "" {
			return errors.New("invalid control state: empty document manifest key")
		}
		if !equalStrings(normalizeStrings(manifest.GrantedSpaceIDs), manifest.GrantedSpaceIDs) {
			return fmt.Errorf("invalid control state: document %q has a non-normalized access snapshot", documentID)
		}
		if manifest.Tombstoned {
			if manifest.ActiveVersionID != "" || manifest.TombstoneRevision == 0 ||
				manifest.LifecycleRevision < manifest.TombstoneRevision {
				return fmt.Errorf("invalid control state: document %q has an inconsistent tombstone", documentID)
			}
		}
		if manifest.ActiveVersionID != "" {
			active, ok := state.VersionsByKey[documentVersionKey(documentID, manifest.ActiveVersionID)]
			if !ok || active.State.OwnerSpaceID != manifest.OwnerSpaceID || manifest.Tombstoned {
				return fmt.Errorf("invalid control state: document %q has an invalid active version", documentID)
			}
		}
	}
	for storageID, pending := range state.PendingVectorDeletes {
		if storageID == "" || !pending {
			return fmt.Errorf("invalid control state: pending vector delete %q is invalid", storageID)
		}
		if _, live := state.VersionsByStore[storageID]; live {
			return fmt.Errorf("invalid control state: pending vector delete %q is still live", storageID)
		}
	}
	for storageID, pending := range state.PendingVectorWrites {
		if storageID == "" || pending.OperationID == "" || pending.Fingerprint == "" || pending.LeaseExpiresAtUnixMilli <= 0 {
			return fmt.Errorf("invalid control state: pending vector write %q is incomplete", storageID)
		}
		if _, live := state.VersionsByStore[storageID]; live {
			return fmt.Errorf("invalid control state: pending vector write %q is already live", storageID)
		}
		if state.PendingVectorDeletes[storageID] {
			return fmt.Errorf("invalid control state: vector %q is pending write and delete", storageID)
		}
		if _, committed := state.Operations[pending.OperationID]; committed {
			return fmt.Errorf("invalid control state: pending vector write %q has a committed operation", storageID)
		}
	}
	for operationID, operation := range state.Operations {
		if operationID == "" || operation.Fingerprint == "" {
			return fmt.Errorf("invalid control state: operation %q is incomplete", operationID)
		}
		if _, err := controlOperationValue(operation); err != nil {
			return fmt.Errorf("invalid control state: operation %q: %w", operationID, err)
		}
	}
	return nil
}

func controlOperationFromRecord(record operationRecord) (ControlOperation, error) {
	operation := ControlOperation{Kind: record.kind, Fingerprint: record.fingerprint}
	switch result := record.result.(type) {
	case DocumentVersionState:
		resultCopy := result
		operation.Result.DocumentVersion = &resultCopy
	case DocumentAccessState:
		resultCopy := cloneDocumentAccessState(result)
		operation.Result.DocumentAccess = &resultCopy
	case DeleteDocumentVersionResult:
		resultCopy := result
		operation.Result.DeleteDocumentVersion = &resultCopy
	case DeleteDocumentResult:
		resultCopy := result
		operation.Result.DeleteDocument = &resultCopy
	default:
		return ControlOperation{}, fmt.Errorf("unsupported operation result %T", record.result)
	}
	if _, err := controlOperationValue(operation); err != nil {
		return ControlOperation{}, err
	}
	return operation, nil
}

func controlOperationValue(operation ControlOperation) (any, error) {
	count := 0
	if operation.Result.DocumentVersion != nil {
		count++
	}
	if operation.Result.DocumentAccess != nil {
		count++
	}
	if operation.Result.DeleteDocumentVersion != nil {
		count++
	}
	if operation.Result.DeleteDocument != nil {
		count++
	}
	if count != 1 {
		return nil, errors.New("operation result must contain exactly one typed value")
	}

	switch operation.Kind {
	case operationIndexDocumentVersion, operationActivateDocumentVersion:
		if operation.Result.DocumentVersion == nil {
			return nil, fmt.Errorf("operation kind %q requires a document version result", operation.Kind)
		}
		return *operation.Result.DocumentVersion, nil
	case operationUpdateDocumentAccess:
		if operation.Result.DocumentAccess == nil {
			return nil, fmt.Errorf("operation kind %q requires a document access result", operation.Kind)
		}
		return cloneDocumentAccessState(*operation.Result.DocumentAccess), nil
	case operationDeleteDocumentVersion:
		if operation.Result.DeleteDocumentVersion == nil {
			return nil, fmt.Errorf("operation kind %q requires a delete-version result", operation.Kind)
		}
		return *operation.Result.DeleteDocumentVersion, nil
	case operationDeleteDocument:
		if operation.Result.DeleteDocument == nil {
			return nil, fmt.Errorf("operation kind %q requires a delete-document result", operation.Kind)
		}
		return *operation.Result.DeleteDocument, nil
	default:
		return nil, fmt.Errorf("unknown operation kind %q", operation.Kind)
	}
}

// readSnapshot returns the snapshot a read-only RPC should serve.
//
// The steady state costs one control-generation probe and no lock: callers never
// wait for a writer, never load the complete control plane, and never touch the
// vector store. Only two things change that:
//
//   - the persisted generation moved, so the published snapshot is stale and
//     must be reloaded. Reloads are serialized by reloadMu, not by the writer
//     lock, so a reader never waits behind a writer's vector I/O.
//   - the snapshot still needs control-plane maintenance (an expired write
//     intent or a pending delete). That is opportunistic: a reader tries the
//     writer lock without waiting and serves the current snapshot when a writer
//     is busy, leaving the cleanup to that writer or to a later request.
func (s *DocumentIndexService) readSnapshot(ctx context.Context) (*controlSnapshot, error) {
	snapshot := s.snapshot.Load()
	changed, err := s.controlStoreChanged(ctx, snapshot)
	if err != nil {
		return nil, err
	}
	if changed {
		s.reloadMu.Lock()
		defer s.reloadMu.Unlock()
		return s.reloadLocked(ctx, s.snapshot.Load())
	}
	if !snapshot.needsMaintenance(time.Now().UnixMilli()) {
		return snapshot, nil
	}
	if !s.writeMu.TryLock() {
		return snapshot, nil
	}
	defer s.writeMu.Unlock()
	s.reloadMu.Lock()
	defer s.reloadMu.Unlock()
	return s.reloadLocked(ctx, s.snapshot.Load())
}

func (s *DocumentIndexService) controlStoreChanged(ctx context.Context, snapshot *controlSnapshot) (bool, error) {
	generation, err := s.controlStore.Generation(ctx)
	if err != nil {
		return false, fmt.Errorf("%w: read control generation: %w", ErrControlStoreUnavailable, err)
	}
	if generation < snapshot.generation {
		return false, fmt.Errorf(
			"%w: control generation regressed from %d to %d",
			ErrControlStoreUnavailable,
			snapshot.generation,
			generation,
		)
	}
	return generation != snapshot.generation, nil
}

// lockWrite serializes control-plane mutations and returns the snapshot they
// must build on. Vector and control-store I/O performed while it is held blocks
// other writers only, never readers.
func (s *DocumentIndexService) lockWrite(ctx context.Context) (*controlSnapshot, error) {
	s.writeMu.Lock()
	s.reloadMu.Lock()
	defer s.reloadMu.Unlock()
	snapshot, err := s.reloadLocked(ctx, s.snapshot.Load())
	if err != nil {
		s.writeMu.Unlock()
		return nil, err
	}
	return snapshot, nil
}

func (s *DocumentIndexService) unlockWrite() {
	s.writeMu.Unlock()
}

// reloadLocked loads the durable control plane, fails closed on regression,
// finishes pending vector maintenance, and publishes the result. The caller must
// hold the writer lock.
func (s *DocumentIndexService) reloadLocked(ctx context.Context, current *controlSnapshot) (*controlSnapshot, error) {
	state, err := s.controlStore.Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: refresh control state: %w", ErrControlStoreUnavailable, err)
	}
	if state.Generation < current.generation {
		return nil, fmt.Errorf(
			"%w: control generation regressed from %d to %d",
			ErrControlStoreUnavailable,
			current.generation,
			state.Generation,
		)
	}
	loaded, err := snapshotFromControlState(state)
	if err != nil {
		return nil, fmt.Errorf("%w: refresh control state: %w", ErrControlStoreUnavailable, err)
	}
	s.publish(loaded)
	if err := s.reconcilePendingVectorWrites(ctx, loaded); err != nil {
		return nil, err
	}
	// Fencing an expired intent publishes a new snapshot that carries the pending
	// delete, so the physical cleanup pass must read the newest one: passing the
	// pre-fence snapshot would skip it and leave the orphaned vectors behind.
	if err := s.reconcilePendingVectorDeletes(ctx, s.snapshot.Load()); err != nil {
		return nil, err
	}
	return s.snapshot.Load(), nil
}

// commit persists a writer-private snapshot with compare-and-swap and publishes
// it only after the durable write succeeded. A conflict or failure simply leaves
// the previous snapshot in place, so there is no rollback path and no window in
// which readers observe a state that was never persisted.
func (s *DocumentIndexService) commit(ctx context.Context, previous, next *controlSnapshot) error {
	next.generation = previous.generation
	generation, err := s.controlStore.Save(ctx, previous.generation, next.toControlState())
	if err != nil {
		if errors.Is(err, ErrControlStoreConflict) {
			return fmt.Errorf("%w: persist control state: %v", ErrControlStoreConflict, err)
		}
		return fmt.Errorf("%w: persist control state: %w", ErrControlStoreUnavailable, err)
	}
	next.generation = generation
	s.publish(next)
	return nil
}

// publish makes snapshot current for readers and asks the projection reconciler
// to bring the vector-store projection up to this generation.
//
// Publication never moves backwards: a reader that loaded the control plane
// while a writer was committing could otherwise re-publish the older state it
// read before that commit. Generation is the total order here, so the newest
// persisted snapshot always wins.
func (s *DocumentIndexService) publish(snapshot *controlSnapshot) {
	for {
		current := s.snapshot.Load()
		if current != nil && snapshot.generation < current.generation {
			return
		}
		if s.snapshot.CompareAndSwap(current, snapshot) {
			break
		}
	}
	s.signalProjection()
}

// reconcilePendingVectorWrites fences expired write intents with a durable
// pending-delete compare-and-swap. Physical cleanup runs only after that claim
// wins, so it cannot delete a vector that another instance just made live.
func (s *DocumentIndexService) reconcilePendingVectorWrites(ctx context.Context, snapshot *controlSnapshot) error {
	if len(snapshot.pendingVectorWrites) == 0 {
		return nil
	}
	now := time.Now().UnixMilli()
	expired := false
	for _, pending := range snapshot.pendingVectorWrites {
		if pending.LeaseExpiresAtUnixMilli <= now {
			expired = true
			break
		}
	}
	if !expired {
		return nil
	}
	next := snapshot.clone()
	for storageID, pending := range next.pendingVectorWrites {
		if pending.LeaseExpiresAtUnixMilli > now {
			continue
		}
		delete(next.pendingVectorWrites, storageID)
		next.pendingVectorDeletes[storageID] = struct{}{}
	}
	if err := s.commit(ctx, snapshot, next); err != nil {
		return fmt.Errorf("claim expired vector write cleanup: %w", err)
	}
	return nil
}

// reconcilePendingVectorDeletes performs only physical cleanup. The logical
// delete and its replay result are already durable, so a vector-store failure
// leaves an invisible, retryable orphan rather than resurrecting data.
func (s *DocumentIndexService) reconcilePendingVectorDeletes(ctx context.Context, snapshot *controlSnapshot) error {
	if len(snapshot.pendingVectorDeletes) == 0 {
		return nil
	}
	next := snapshot.clone()
	removed := false
	for storageID := range next.pendingVectorDeletes {
		if err := s.core.DeleteIndexedDocument(ctx, storageID); err != nil {
			continue
		}
		delete(next.pendingVectorDeletes, storageID)
		removed = true
	}
	if !removed {
		return nil
	}
	if err := s.commit(ctx, snapshot, next); err != nil {
		return fmt.Errorf("persist vector cleanup progress: %w", err)
	}
	return nil
}

// abandonVectorWrite releases an unfinished write intent after a failed or
// expired vector write, then cleans the orphaned vectors.
func (s *DocumentIndexService) abandonVectorWrite(
	ctx context.Context,
	snapshot *controlSnapshot,
	storageID string,
) (*controlSnapshot, error) {
	next := snapshot.clone()
	delete(next.pendingVectorWrites, storageID)
	next.pendingVectorDeletes[storageID] = struct{}{}
	if err := s.commit(ctx, snapshot, next); err != nil {
		return nil, fmt.Errorf("claim abandoned vector write: %w", err)
	}
	if err := s.reconcilePendingVectorDeletes(ctx, next); err != nil {
		return nil, fmt.Errorf("clean abandoned vector write: %w", err)
	}
	return s.snapshot.Load(), nil
}
