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
type ControlStore interface {
	Load(context.Context) (ControlState, error)
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

func (s *DocumentIndexService) captureControlStateLocked() (ControlState, error) {
	state := newControlState()
	state.Generation = s.controlGeneration
	for key, version := range s.versionsByKey {
		state.VersionsByKey[key] = ControlVersion{
			State:     version.state,
			StorageID: version.storageID,
			SourceURI: version.sourceURI,
			Metadata:  cloneStringMap(version.metadata),
		}
	}
	for storageID, key := range s.versionsByStore {
		state.VersionsByStore[storageID] = key
	}
	for key, fingerprint := range s.versionFingerprints {
		state.VersionFingerprints[key] = ControlVersionFingerprint{
			ContentSHA256: fingerprint.contentSHA256,
			OwnerSpaceID:  fingerprint.ownerSpaceID,
		}
	}
	for documentID, manifest := range s.manifests {
		documentDeleteResults := make(map[uint64]DeleteDocumentResult, len(manifest.documentDeleteResults))
		for revision, result := range manifest.documentDeleteResults {
			documentDeleteResults[revision] = result
		}
		versionDeleteResults := make(map[string]map[uint64]DeleteDocumentVersionResult, len(manifest.versionDeleteResults))
		for versionID, results := range manifest.versionDeleteResults {
			resultCopies := make(map[uint64]DeleteDocumentVersionResult, len(results))
			for revision, result := range results {
				resultCopies[revision] = result
			}
			versionDeleteResults[versionID] = resultCopies
		}
		state.Manifests[documentID] = ControlDocumentManifest{
			OwnerSpaceID:          manifest.ownerSpaceID,
			ActiveVersionID:       manifest.activeVersionID,
			ActivationVersionID:   manifest.activationVersionID,
			ActivationRevision:    manifest.activationRevision,
			ActivationState:       manifest.activationState,
			AccessRevision:        manifest.accessRevision,
			AuthenticatedPublic:   manifest.authenticatedPublic,
			GrantedSpaceIDs:       cloneStrings(manifest.grantedSpaceIDs),
			LifecycleRevision:     manifest.lifecycleRevision,
			TombstoneRevision:     manifest.tombstoneRevision,
			Tombstoned:            manifest.tombstoned,
			DocumentDeleteResults: documentDeleteResults,
			VersionDeleteResults:  versionDeleteResults,
		}
	}
	for operationID, record := range s.operations {
		operation, err := controlOperationFromRecord(record)
		if err != nil {
			return ControlState{}, fmt.Errorf("capture operation %q: %w", operationID, err)
		}
		state.Operations[operationID] = operation
	}
	for storageID := range s.pendingVectorDeletes {
		state.PendingVectorDeletes[storageID] = true
	}
	for storageID, pending := range s.pendingVectorWrites {
		state.PendingVectorWrites[storageID] = pending
	}
	return cloneControlState(state)
}

func (s *DocumentIndexService) restoreControlStateLocked(state ControlState) error {
	state, err := cloneControlState(state)
	if err != nil {
		return err
	}
	versionsByKey := make(map[string]contractVersion, len(state.VersionsByKey))
	for key, version := range state.VersionsByKey {
		versionsByKey[key] = contractVersion{
			state:     version.State,
			storageID: version.StorageID,
			sourceURI: version.SourceURI,
			metadata:  cloneStringMap(version.Metadata),
		}
	}
	versionsByStore := make(map[string]string, len(state.VersionsByStore))
	for storageID, key := range state.VersionsByStore {
		versionsByStore[storageID] = key
	}
	versionFingerprints := make(map[string]versionFingerprint, len(state.VersionFingerprints))
	for key, fingerprint := range state.VersionFingerprints {
		versionFingerprints[key] = versionFingerprint{
			contentSHA256: fingerprint.ContentSHA256,
			ownerSpaceID:  fingerprint.OwnerSpaceID,
		}
	}
	manifests := make(map[string]*documentManifest, len(state.Manifests))
	for documentID, manifest := range state.Manifests {
		documentDeleteResults := make(map[uint64]DeleteDocumentResult, len(manifest.DocumentDeleteResults))
		for revision, result := range manifest.DocumentDeleteResults {
			documentDeleteResults[revision] = result
		}
		versionDeleteResults := make(map[string]map[uint64]DeleteDocumentVersionResult, len(manifest.VersionDeleteResults))
		for versionID, results := range manifest.VersionDeleteResults {
			resultCopies := make(map[uint64]DeleteDocumentVersionResult, len(results))
			for revision, result := range results {
				resultCopies[revision] = result
			}
			versionDeleteResults[versionID] = resultCopies
		}
		manifests[documentID] = &documentManifest{
			ownerSpaceID:          manifest.OwnerSpaceID,
			activeVersionID:       manifest.ActiveVersionID,
			activationVersionID:   manifest.ActivationVersionID,
			activationRevision:    manifest.ActivationRevision,
			activationState:       manifest.ActivationState,
			accessRevision:        manifest.AccessRevision,
			authenticatedPublic:   manifest.AuthenticatedPublic,
			grantedSpaceIDs:       cloneStrings(manifest.GrantedSpaceIDs),
			lifecycleRevision:     manifest.LifecycleRevision,
			tombstoneRevision:     manifest.TombstoneRevision,
			tombstoned:            manifest.Tombstoned,
			documentDeleteResults: documentDeleteResults,
			versionDeleteResults:  versionDeleteResults,
		}
	}
	operations := make(map[string]operationRecord, len(state.Operations))
	for operationID, operation := range state.Operations {
		result, err := controlOperationValue(operation)
		if err != nil {
			return fmt.Errorf("restore operation %q: %w", operationID, err)
		}
		operations[operationID] = operationRecord{
			kind:        operation.Kind,
			fingerprint: operation.Fingerprint,
			result:      result,
		}
	}
	pendingVectorDeletes := make(map[string]struct{}, len(state.PendingVectorDeletes))
	for storageID := range state.PendingVectorDeletes {
		pendingVectorDeletes[storageID] = struct{}{}
	}
	pendingVectorWrites := make(map[string]ControlPendingVectorWrite, len(state.PendingVectorWrites))
	for storageID, pending := range state.PendingVectorWrites {
		pendingVectorWrites[storageID] = pending
	}

	s.versionsByKey = versionsByKey
	s.versionsByStore = versionsByStore
	s.versionFingerprints = versionFingerprints
	s.manifests = manifests
	s.operations = operations
	s.pendingVectorWrites = pendingVectorWrites
	s.pendingVectorDeletes = pendingVectorDeletes
	s.controlGeneration = state.Generation
	return nil
}

func (s *DocumentIndexService) refreshControlStateLocked(ctx context.Context) error {
	state, err := s.controlStore.Load(ctx)
	if err != nil {
		return fmt.Errorf("%w: refresh control state: %w", ErrControlStoreUnavailable, err)
	}
	if state.Generation < s.controlGeneration {
		return fmt.Errorf(
			"%w: control generation regressed from %d to %d",
			ErrControlStoreUnavailable,
			s.controlGeneration,
			state.Generation,
		)
	}
	if err := s.restoreControlStateLocked(state); err != nil {
		return fmt.Errorf("%w: refresh control state: %w", ErrControlStoreUnavailable, err)
	}
	if err := s.reconcilePendingVectorWritesLocked(ctx); err != nil {
		return err
	}
	return s.reconcilePendingVectorDeletesLocked(ctx)
}

// reconcilePendingVectorWritesLocked first fences expired intents with a
// durable pending-delete CAS. Physical cleanup runs only after that claim wins,
// so it cannot delete a vector that another instance just made live.
func (s *DocumentIndexService) reconcilePendingVectorWritesLocked(ctx context.Context) error {
	if len(s.pendingVectorWrites) == 0 {
		return nil
	}
	before, err := s.captureControlStateLocked()
	if err != nil {
		return fmt.Errorf("%w: capture vector write cleanup state: %w", ErrControlStoreUnavailable, err)
	}
	now := time.Now().UnixMilli()
	claimed := false
	for storageID, pending := range s.pendingVectorWrites {
		if pending.LeaseExpiresAtUnixMilli > now {
			continue
		}
		delete(s.pendingVectorWrites, storageID)
		s.pendingVectorDeletes[storageID] = struct{}{}
		claimed = true
	}
	if !claimed {
		return nil
	}
	if err := s.persistControlMutationLocked(ctx, before); err != nil {
		return fmt.Errorf("claim expired vector write cleanup: %w", err)
	}
	return nil
}

// reconcilePendingVectorDeletesLocked performs only physical cleanup. The
// logical delete and its replay result are already durable, so a vector-store
// failure leaves an invisible, retryable orphan rather than resurrecting data.
func (s *DocumentIndexService) reconcilePendingVectorDeletesLocked(ctx context.Context) error {
	if len(s.pendingVectorDeletes) == 0 {
		return nil
	}
	before, err := s.captureControlStateLocked()
	if err != nil {
		return fmt.Errorf("%w: capture vector cleanup state: %w", ErrControlStoreUnavailable, err)
	}
	removed := false
	for storageID := range s.pendingVectorDeletes {
		if err := s.core.DeleteIndexedDocument(ctx, storageID); err != nil {
			continue
		}
		delete(s.pendingVectorDeletes, storageID)
		removed = true
	}
	if !removed {
		return nil
	}
	if err := s.persistControlMutationLocked(ctx, before); err != nil {
		return fmt.Errorf("persist vector cleanup progress: %w", err)
	}
	return nil
}

func (s *DocumentIndexService) persistControlMutationLocked(ctx context.Context, before ControlState) error {
	next, err := s.captureControlStateLocked()
	if err == nil {
		var nextGeneration uint64
		nextGeneration, err = s.controlStore.Save(ctx, before.Generation, next)
		if err == nil {
			s.controlGeneration = nextGeneration
			return nil
		}
	}

	if restoreErr := s.restoreControlStateLocked(before); restoreErr != nil {
		err = errors.Join(err, fmt.Errorf("restore previous control state: %w", restoreErr))
	}
	if errors.Is(err, ErrControlStoreConflict) {
		return fmt.Errorf("%w: persist control state: %v", ErrControlStoreConflict, err)
	}
	return fmt.Errorf("%w: persist control state: %w", ErrControlStoreUnavailable, err)
}
