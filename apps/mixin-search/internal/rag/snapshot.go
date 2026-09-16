package rag

import (
	"fmt"
	"sort"
)

// controlSnapshot is an immutable view of the contract control plane.
//
// The service publishes a new snapshot with an atomic pointer swap and never
// mutates a published one, so read RPCs need no lock at all: they load the
// current pointer and answer from it. Write RPCs are serialized by a separate
// writer lock and apply their changes to a private copy which is published only
// after the durable compare-and-swap succeeds.
//
// This replaces the earlier design in which every request loaded the complete
// control plane, cloned it through a JSON round trip, and held one global
// exclusive lock across PostgreSQL and vector-store calls.
type controlSnapshot struct {
	generation           uint64
	versionsByKey        map[string]contractVersion
	versionsByStore      map[string]string
	versionFingerprints  map[string]versionFingerprint
	manifests            map[string]*documentManifest
	operations           map[string]operationRecord
	pendingVectorWrites  map[string]ControlPendingVectorWrite
	pendingVectorDeletes map[string]struct{}
}

func newControlSnapshot() *controlSnapshot {
	return &controlSnapshot{
		versionsByKey:        make(map[string]contractVersion),
		versionsByStore:      make(map[string]string),
		versionFingerprints:  make(map[string]versionFingerprint),
		manifests:            make(map[string]*documentManifest),
		operations:           make(map[string]operationRecord),
		pendingVectorWrites:  make(map[string]ControlPendingVectorWrite),
		pendingVectorDeletes: make(map[string]struct{}),
	}
}

// clone returns a private copy a writer may mutate without disturbing readers
// that still hold the published snapshot.
func (snapshot *controlSnapshot) clone() *controlSnapshot {
	clone := &controlSnapshot{
		generation:           snapshot.generation,
		versionsByKey:        make(map[string]contractVersion, len(snapshot.versionsByKey)),
		versionsByStore:      make(map[string]string, len(snapshot.versionsByStore)),
		versionFingerprints:  make(map[string]versionFingerprint, len(snapshot.versionFingerprints)),
		manifests:            make(map[string]*documentManifest, len(snapshot.manifests)),
		operations:           make(map[string]operationRecord, len(snapshot.operations)),
		pendingVectorWrites:  make(map[string]ControlPendingVectorWrite, len(snapshot.pendingVectorWrites)),
		pendingVectorDeletes: make(map[string]struct{}, len(snapshot.pendingVectorDeletes)),
	}
	for key, version := range snapshot.versionsByKey {
		version.metadata = cloneStringMap(version.metadata)
		clone.versionsByKey[key] = version
	}
	for storageID, key := range snapshot.versionsByStore {
		clone.versionsByStore[storageID] = key
	}
	for key, fingerprint := range snapshot.versionFingerprints {
		clone.versionFingerprints[key] = fingerprint
	}
	for documentID, manifest := range snapshot.manifests {
		clone.manifests[documentID] = manifest.clone()
	}
	for operationID, record := range snapshot.operations {
		record.result = cloneOperationResult(record.result)
		clone.operations[operationID] = record
	}
	for operationID, pending := range snapshot.pendingVectorWrites {
		clone.pendingVectorWrites[operationID] = pending
	}
	for storageID := range snapshot.pendingVectorDeletes {
		clone.pendingVectorDeletes[storageID] = struct{}{}
	}
	return clone
}

func (manifest *documentManifest) clone() *documentManifest {
	if manifest == nil {
		return nil
	}
	clone := *manifest
	clone.grantedSpaceIDs = cloneStrings(manifest.grantedSpaceIDs)
	clone.documentDeleteResults = make(map[uint64]DeleteDocumentResult, len(manifest.documentDeleteResults))
	for revision, result := range manifest.documentDeleteResults {
		clone.documentDeleteResults[revision] = result
	}
	clone.versionDeleteResults = make(map[string]map[uint64]DeleteDocumentVersionResult, len(manifest.versionDeleteResults))
	for versionID, results := range manifest.versionDeleteResults {
		copies := make(map[uint64]DeleteDocumentVersionResult, len(results))
		for revision, result := range results {
			copies[revision] = result
		}
		clone.versionDeleteResults[versionID] = copies
	}
	return &clone
}

// needsMaintenance reports whether this snapshot still requires work that only a
// control-plane write can finish: a lease-expired write intent that must be
// fenced as a pending delete, or a pending delete that must be cleaned up.
//
// A write intent whose lease is still valid is deliberately NOT maintenance: an
// in-flight index write owns it, and treating it as work would make every
// concurrent reader queue behind that writer.
func (snapshot *controlSnapshot) needsMaintenance(nowUnixMilli int64) bool {
	if len(snapshot.pendingVectorDeletes) > 0 {
		return true
	}
	for _, pending := range snapshot.pendingVectorWrites {
		if pending.LeaseExpiresAtUnixMilli <= nowUnixMilli {
			return true
		}
	}
	return false
}

// manifest returns the document manifest, creating an empty one in this
// snapshot when the document is new. Callers must hold a writer-private copy
// whenever the result may be mutated.
func (snapshot *controlSnapshot) manifest(documentID string) *documentManifest {
	manifest := snapshot.manifests[documentID]
	if manifest == nil {
		manifest = &documentManifest{
			documentDeleteResults: make(map[uint64]DeleteDocumentResult),
			versionDeleteResults:  make(map[string]map[uint64]DeleteDocumentVersionResult),
		}
		snapshot.manifests[documentID] = manifest
	}
	return manifest
}

func (snapshot *controlSnapshot) replayOperation(
	operationID string,
	kind string,
	fingerprint string,
) (any, bool, error) {
	record, ok := snapshot.operations[operationID]
	if !ok {
		for _, pending := range snapshot.pendingVectorWrites {
			if pending.OperationID != operationID {
				continue
			}
			if kind != operationIndexDocumentVersion || pending.Fingerprint != fingerprint {
				return nil, false, fmt.Errorf("%w: operation_id %q is already bound to an unfinished index operation", ErrConflict, operationID)
			}
			return nil, false, nil
		}
		return nil, false, nil
	}
	if record.kind != kind || record.fingerprint != fingerprint {
		return nil, false, fmt.Errorf("%w: operation_id %q is already bound to %s", ErrConflict, operationID, record.kind)
	}
	return cloneOperationResult(record.result), true, nil
}

func (snapshot *controlSnapshot) recordOperation(
	operationID string,
	kind string,
	fingerprint string,
	result any,
) {
	snapshot.operations[operationID] = operationRecord{
		kind:        kind,
		fingerprint: fingerprint,
		result:      cloneOperationResult(result),
	}
}

func (snapshot *controlSnapshot) activationRetryState(manifest *documentManifest) DocumentVersionState {
	if version, ok := snapshot.versionsByKey[documentVersionKey(manifest.activationState.DocumentID, manifest.activationVersionID)]; ok {
		return versionState(version, manifest)
	}
	state := manifest.activationState
	state.Status = DocumentVersionIndexed
	state.ActivationRevision = 0
	state.AccessRevision = manifest.accessRevision
	state.LifecycleRevision = manifest.lifecycleRevision
	return state
}

// vectorDocumentControls renders the candidate-level projection the vector store
// needs for lifecycle and authorization filtering.
func (snapshot *controlSnapshot) vectorDocumentControls(storageDomain string) []VectorDocumentControl {
	keys := make([]string, 0, len(snapshot.versionsByKey))
	for key := range snapshot.versionsByKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	controls := make([]VectorDocumentControl, 0, len(keys)+len(snapshot.pendingVectorDeletes))
	projected := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		version := snapshot.versionsByKey[key]
		manifest := snapshot.manifests[version.state.DocumentID]
		if manifest == nil {
			continue
		}
		controls = append(controls, VectorDocumentControl{
			StorageID:           version.storageID,
			StorageDomain:       storageDomain,
			DocumentID:          version.state.DocumentID,
			VersionID:           version.state.VersionID,
			OwnerSpaceID:        version.state.OwnerSpaceID,
			AuthenticatedPublic: manifest.authenticatedPublic,
			GrantedSpaceIDs:     cloneStrings(manifest.grantedSpaceIDs),
			Active:              !manifest.tombstoned && manifest.activeVersionID == version.state.VersionID,
			Tombstoned:          manifest.tombstoned,
			ActivationRevision:  manifest.activationRevision,
			AccessRevision:      manifest.accessRevision,
			LifecycleRevision:   manifest.lifecycleRevision,
			ContentSHA256:       version.state.ContentSHA256,
		})
		projected[version.storageID] = struct{}{}
	}
	for storageID := range snapshot.pendingVectorDeletes {
		if _, ok := projected[storageID]; ok {
			continue
		}
		controls = append(controls, VectorDocumentControl{
			StorageID:     storageID,
			StorageDomain: storageDomain,
			Tombstoned:    true,
		})
	}
	return controls
}

// toControlState renders the persistence model for a control-store save.
func (snapshot *controlSnapshot) toControlState() ControlState {
	state := newControlState()
	state.Generation = snapshot.generation
	for key, version := range snapshot.versionsByKey {
		state.VersionsByKey[key] = ControlVersion{
			State:     version.state,
			StorageID: version.storageID,
			SourceURI: version.sourceURI,
			Metadata:  cloneStringMap(version.metadata),
		}
	}
	for storageID, key := range snapshot.versionsByStore {
		state.VersionsByStore[storageID] = key
	}
	for key, fingerprint := range snapshot.versionFingerprints {
		state.VersionFingerprints[key] = ControlVersionFingerprint{
			ContentSHA256: fingerprint.contentSHA256,
			OwnerSpaceID:  fingerprint.ownerSpaceID,
		}
	}
	for documentID, manifest := range snapshot.manifests {
		documentDeleteResults := make(map[uint64]DeleteDocumentResult, len(manifest.documentDeleteResults))
		for revision, result := range manifest.documentDeleteResults {
			documentDeleteResults[revision] = result
		}
		versionDeleteResults := make(map[string]map[uint64]DeleteDocumentVersionResult, len(manifest.versionDeleteResults))
		for versionID, results := range manifest.versionDeleteResults {
			copies := make(map[uint64]DeleteDocumentVersionResult, len(results))
			for revision, result := range results {
				copies[revision] = result
			}
			versionDeleteResults[versionID] = copies
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
	for operationID, record := range snapshot.operations {
		operation, err := controlOperationFromRecord(record)
		if err != nil {
			// A snapshot only ever holds records this package produced, so this
			// cannot happen; keeping the zero operation would silently persist a
			// state the reader cannot replay, which is worse than failing closed.
			panic(fmt.Sprintf("render operation %q: %v", operationID, err))
		}
		state.Operations[operationID] = operation
	}
	for storageID := range snapshot.pendingVectorDeletes {
		state.PendingVectorDeletes[storageID] = true
	}
	for storageID, pending := range snapshot.pendingVectorWrites {
		state.PendingVectorWrites[storageID] = pending
	}
	return state
}

// snapshotFromControlState validates a persisted state and converts it into the
// in-memory representation. Validation happens once per load, not once per
// persistence, so an incomplete snapshot still fails closed at startup.
func snapshotFromControlState(state ControlState) (*controlSnapshot, error) {
	state, err := cloneControlState(state)
	if err != nil {
		return nil, err
	}
	snapshot := newControlSnapshot()
	snapshot.generation = state.Generation
	for key, version := range state.VersionsByKey {
		snapshot.versionsByKey[key] = contractVersion{
			state:     version.State,
			storageID: version.StorageID,
			sourceURI: version.SourceURI,
			metadata:  cloneStringMap(version.Metadata),
		}
	}
	for storageID, key := range state.VersionsByStore {
		snapshot.versionsByStore[storageID] = key
	}
	for key, fingerprint := range state.VersionFingerprints {
		snapshot.versionFingerprints[key] = versionFingerprint{
			contentSHA256: fingerprint.ContentSHA256,
			ownerSpaceID:  fingerprint.OwnerSpaceID,
		}
	}
	for documentID, manifest := range state.Manifests {
		documentDeleteResults := make(map[uint64]DeleteDocumentResult, len(manifest.DocumentDeleteResults))
		for revision, result := range manifest.DocumentDeleteResults {
			documentDeleteResults[revision] = result
		}
		versionDeleteResults := make(map[string]map[uint64]DeleteDocumentVersionResult, len(manifest.VersionDeleteResults))
		for versionID, results := range manifest.VersionDeleteResults {
			copies := make(map[uint64]DeleteDocumentVersionResult, len(results))
			for revision, result := range results {
				copies[revision] = result
			}
			versionDeleteResults[versionID] = copies
		}
		snapshot.manifests[documentID] = &documentManifest{
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
	for operationID, operation := range state.Operations {
		result, err := controlOperationValue(operation)
		if err != nil {
			return nil, fmt.Errorf("restore operation %q: %w", operationID, err)
		}
		snapshot.operations[operationID] = operationRecord{
			kind:        operation.Kind,
			fingerprint: operation.Fingerprint,
			result:      result,
		}
	}
	for storageID := range state.PendingVectorDeletes {
		snapshot.pendingVectorDeletes[storageID] = struct{}{}
	}
	for storageID, pending := range state.PendingVectorWrites {
		snapshot.pendingVectorWrites[storageID] = pending
	}
	return snapshot, nil
}
