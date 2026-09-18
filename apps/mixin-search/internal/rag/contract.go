package rag

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"mixin-search/internal/controlplane"
)

var (
	ErrNotFound        = errors.New("not found")
	ErrConflict        = errors.New("conflict")
	ErrStaleActivation = errors.New("stale activation revision")
	ErrStaleAccess     = errors.New("stale access revision")
	ErrStaleLifecycle  = errors.New("stale lifecycle revision")
)

type DocumentVersionStatus string

const (
	DocumentVersionIndexed DocumentVersionStatus = "indexed"
	DocumentVersionActive  DocumentVersionStatus = "active"
)

type DocumentVersionState struct {
	DocumentID         string
	VersionID          string
	OwnerSpaceID       string
	Status             DocumentVersionStatus
	ActivationRevision uint64
	AccessRevision     uint64
	LifecycleRevision  uint64
	ChunkCount         int
	ContentSHA256      string
}

type DocumentAccessState struct {
	DocumentID          string
	AccessRevision      uint64
	LifecycleRevision   uint64
	AuthenticatedPublic bool
	GrantedSpaceIDs     []string
}

type IndexDocumentVersionRequest struct {
	OperationID       string
	DocumentID        string
	VersionID         string
	OwnerSpaceID      string
	LifecycleRevision uint64
	Filename          string
	Title             string
	Content           []byte
	ContentSHA256     string
	ChunkSize         int
	Overlap           int
	SourceURI         string
	Metadata          map[string]string
}

type ActivateDocumentVersionRequest struct {
	OperationID               string
	DocumentID                string
	VersionID                 string
	ActivationRevision        uint64
	LifecycleRevision         uint64
	ExpectedPreviousVersionID string
}

type UpdateDocumentAccessRequest struct {
	OperationID         string
	DocumentID          string
	AccessRevision      uint64
	LifecycleRevision   uint64
	AuthenticatedPublic bool
	GrantedSpaceIDs     []string
}

type DeleteDocumentVersionRequest struct {
	OperationID       string
	DocumentID        string
	VersionID         string
	LifecycleRevision uint64
}

type DeleteDocumentVersionResult struct {
	Deleted              bool
	ActiveVersionRemoved bool
}

type DeleteDocumentRequest struct {
	OperationID       string
	DocumentID        string
	LifecycleRevision uint64
}

type DeleteDocumentResult struct {
	Tombstoned        bool
	LifecycleRevision uint64
}

type GetDocumentVersionStateRequest struct {
	DocumentID string
	VersionID  string
}

type GetDocumentVersionStateResult struct {
	Exists bool
	State  DocumentVersionState
}

type SearchDocumentsRequest struct {
	Query              string
	AllowedSpaceIDs    []string
	AllowedDocumentIDs []string
	TopK               int
}

type SearchDocumentsResult struct {
	Query     string
	Hits      []SearchHit
	Truncated bool
}

type contractVersion struct {
	state     DocumentVersionState
	storageID string
	sourceURI string
	metadata  map[string]string
}

type versionFingerprint struct {
	contentSHA256 string
	ownerSpaceID  string
}

type operationRecord struct {
	kind        string
	fingerprint string
	result      any
}

// documentManifest retains independent fencing high-water marks even after
// every derived chunk has been deleted. lifecycleRevision is the newest live
// generation or tombstone observed; tombstoneRevision identifies the delete
// that currently closes the document.
type documentManifest struct {
	ownerSpaceID          string
	activeVersionID       string
	activationVersionID   string
	activationRevision    uint64
	activationState       DocumentVersionState
	accessRevision        uint64
	authenticatedPublic   bool
	grantedSpaceIDs       []string
	lifecycleRevision     uint64
	tombstoneRevision     uint64
	tombstoned            bool
	documentDeleteResults map[uint64]DeleteDocumentResult
	versionDeleteResults  map[string]map[uint64]DeleteDocumentVersionResult
}

// DocumentIndexService implements the mixin-search/v1 contract over the shared
// Eino workflows. Its control plane is an immutable snapshot: readers load the
// published pointer without taking a lock, while writers serialize on writeMu
// and publish a private copy only after the durable compare-and-swap succeeds.
type DocumentIndexService struct {
	core          *Service
	controlStore  ControlStore
	storageDomain string

	// state is the published control snapshot. Publication is monotonic in
	// generation and goes through the same corpus-agnostic mechanism the chat
	// corpus uses; only the mechanism is shared, never the state.
	state *controlplane.State[controlSnapshot]
	// writeMu serializes control-plane mutations; readers never take it.
	writeMu sync.Mutex
	// reloadMu serializes control-plane reloads. It is separate from writeMu so a
	// reader whose snapshot is stale does not wait behind a writer's vector I/O.
	reloadMu sync.Mutex

	// projection tracks how far the vector store has been converged to the
	// published generation, and is driven by the background reconciler.
	projection *controlplane.Projection
}

func NewDocumentIndexService(core *Service) (*DocumentIndexService, error) {
	return NewDocumentIndexServiceWithControlStore(context.Background(), core, NewMemoryControlStore())
}

// NewDocumentIndexServiceWithControlStore restores the complete control plane
// before returning a usable service. A load or validation failure prevents the
// service from starting, so vector chunks can never be exposed without their
// durable authorization and lifecycle state.
func NewDocumentIndexServiceWithControlStore(
	ctx context.Context,
	core *Service,
	controlStore ControlStore,
) (*DocumentIndexService, error) {
	if core == nil {
		return nil, errors.New("rag core service is required")
	}
	if ctx == nil {
		return nil, errors.New("control store load context is required")
	}
	if controlStore == nil {
		return nil, errors.New("control store is required")
	}
	state, err := controlStore.Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: load control state: %w", ErrControlStoreUnavailable, err)
	}
	storageDomain := strings.TrimSpace(controlStore.StorageDomain())
	if storageDomain == "" {
		return nil, errors.New("control store storage domain is required")
	}
	restored, err := snapshotFromControlState(state)
	if err != nil {
		return nil, fmt.Errorf("%w: restore control state: %w", ErrControlStoreUnavailable, err)
	}
	service := &DocumentIndexService{
		core:          core,
		controlStore:  controlStore,
		storageDomain: storageDomain,
		state:         controlplane.NewState(restored, restored.generation),
		projection:    controlplane.NewProjection(),
	}
	return service, nil
}

func (s *DocumentIndexService) IndexDocumentVersion(
	ctx context.Context,
	request IndexDocumentVersionRequest,
) (DocumentVersionState, error) {
	request.OperationID = strings.TrimSpace(request.OperationID)
	request.DocumentID = strings.TrimSpace(request.DocumentID)
	request.VersionID = strings.TrimSpace(request.VersionID)
	request.OwnerSpaceID = strings.TrimSpace(request.OwnerSpaceID)
	request.Filename = strings.TrimSpace(request.Filename)
	if request.OperationID == "" || request.DocumentID == "" || request.VersionID == "" ||
		request.OwnerSpaceID == "" || request.Filename == "" || len(request.Content) == 0 {
		return DocumentVersionState{}, fmt.Errorf("%w: operation_id, document_id, version_id, owner_space_id, filename and content are required", ErrInvalidInput)
	}

	digestBytes := sha256.Sum256(request.Content)
	digest := hex.EncodeToString(digestBytes[:])
	if request.ContentSHA256 != "" && !strings.EqualFold(strings.TrimSpace(request.ContentSHA256), digest) {
		return DocumentVersionState{}, fmt.Errorf("%w: content_sha256 does not match content", ErrInvalidInput)
	}
	request.SourceURI = strings.TrimSpace(request.SourceURI)
	request.Metadata = cloneStringMap(request.Metadata)
	fingerprint := operationFingerprint(struct {
		DocumentID        string
		VersionID         string
		OwnerSpaceID      string
		LifecycleRevision uint64
		Filename          string
		Title             string
		ContentSHA256     string
		ChunkSize         int
		Overlap           int
		SourceURI         string
		Metadata          map[string]string
	}{
		DocumentID:        request.DocumentID,
		VersionID:         request.VersionID,
		OwnerSpaceID:      request.OwnerSpaceID,
		LifecycleRevision: request.LifecycleRevision,
		Filename:          request.Filename,
		Title:             request.Title,
		ContentSHA256:     digest,
		ChunkSize:         request.ChunkSize,
		Overlap:           request.Overlap,
		SourceURI:         request.SourceURI,
		Metadata:          request.Metadata,
	})

	locked, err := s.lockWrite(ctx)
	if err != nil {
		return DocumentVersionState{}, err
	}
	defer s.unlockWrite()
	snapshot := locked
	next := snapshot.clone()
	if replay, ok, err := next.replayOperation(request.OperationID, "index_document_version", fingerprint); err != nil {
		return DocumentVersionState{}, err
	} else if ok {
		return replay.(DocumentVersionState), nil
	}
	manifest := next.manifest(request.DocumentID)
	if err := validateLiveLifecycle(manifest, request.LifecycleRevision); err != nil {
		return DocumentVersionState{}, err
	}
	if manifest.ownerSpaceID != "" && manifest.ownerSpaceID != request.OwnerSpaceID {
		return DocumentVersionState{}, fmt.Errorf("%w: document owner space is %q, requested %q", ErrConflict, manifest.ownerSpaceID, request.OwnerSpaceID)
	}

	key := documentVersionKey(request.DocumentID, request.VersionID)
	if fingerprint, ok := next.versionFingerprints[key]; ok &&
		(fingerprint.contentSHA256 != digest || fingerprint.ownerSpaceID != request.OwnerSpaceID) {
		return DocumentVersionState{}, fmt.Errorf("%w: document version already exists with different content or owner space", ErrConflict)
	}
	if existing, ok := next.versionsByKey[key]; ok {
		advanceLiveLifecycle(manifest, request.LifecycleRevision)
		state := versionState(existing, manifest)
		next.recordOperation(request.OperationID, "index_document_version", fingerprint, state)
		if err := s.commit(ctx, snapshot, next); err != nil {
			return DocumentVersionState{}, err
		}
		return state, nil
	}

	storageID := storageDocumentID(s.storageDomain, request.DocumentID, request.VersionID, request.OperationID)
	pending, ok := next.pendingVectorWrites[storageID]
	if ok {
		if pending.OperationID != request.OperationID || pending.Fingerprint != fingerprint {
			return DocumentVersionState{}, fmt.Errorf("%w: operation_id %q is already bound to another index payload", ErrConflict, request.OperationID)
		}
	} else {
		pending = ControlPendingVectorWrite{
			OperationID:             request.OperationID,
			Fingerprint:             fingerprint,
			LeaseExpiresAtUnixMilli: time.Now().Add(pendingVectorWriteLease).UnixMilli(),
		}
		next.pendingVectorWrites[storageID] = pending
		if err := s.commit(ctx, snapshot, next); err != nil {
			return DocumentVersionState{}, err
		}
		snapshot = next
		next = snapshot.clone()
		// The clone deep-copies manifests, so the previous pointer now belongs to
		// the published snapshot and must not be mutated any further.
		manifest = next.manifest(request.DocumentID)
	}
	vectorWriteContext, cancelVectorWrite := context.WithDeadline(
		ctx,
		time.UnixMilli(pending.LeaseExpiresAtUnixMilli),
	)
	defer cancelVectorWrite()
	result, err := s.core.IngestDocument(vectorWriteContext, IngestDocumentRequest{
		DocumentID: storageID,
		Filename:   request.Filename,
		Title:      request.Title,
		Content:    request.Content,
		ChunkSize:  request.ChunkSize,
		Overlap:    request.Overlap,
	})
	if err != nil {
		if _, persistErr := s.abandonVectorWrite(ctx, snapshot, storageID); persistErr != nil {
			return DocumentVersionState{}, errors.Join(err, persistErr)
		}
		return DocumentVersionState{}, err
	}
	if time.Now().UnixMilli() >= pending.LeaseExpiresAtUnixMilli {
		leaseErr := fmt.Errorf("%w: vector write intent lease expired", context.DeadlineExceeded)
		if _, persistErr := s.abandonVectorWrite(ctx, snapshot, storageID); persistErr != nil {
			return DocumentVersionState{}, errors.Join(leaseErr, persistErr)
		}
		return DocumentVersionState{}, leaseErr
	}

	version := contractVersion{
		state: DocumentVersionState{
			DocumentID:    request.DocumentID,
			VersionID:     request.VersionID,
			OwnerSpaceID:  request.OwnerSpaceID,
			Status:        DocumentVersionIndexed,
			ChunkCount:    result.ChunkCount,
			ContentSHA256: digest,
		},
		storageID: storageID,
		sourceURI: strings.TrimSpace(request.SourceURI),
		metadata:  cloneStringMap(request.Metadata),
	}
	next.versionsByKey[key] = version
	next.versionsByStore[storageID] = key
	delete(next.pendingVectorWrites, storageID)
	next.versionFingerprints[key] = versionFingerprint{contentSHA256: digest, ownerSpaceID: request.OwnerSpaceID}
	if manifest.ownerSpaceID == "" {
		manifest.ownerSpaceID = request.OwnerSpaceID
	}
	advanceLiveLifecycle(manifest, request.LifecycleRevision)
	state := versionState(version, manifest)
	next.recordOperation(request.OperationID, "index_document_version", fingerprint, state)
	if err := s.commit(ctx, snapshot, next); err != nil {
		return DocumentVersionState{}, err
	}
	return state, nil
}

func (s *DocumentIndexService) ActivateDocumentVersion(
	ctx context.Context,
	request ActivateDocumentVersionRequest,
) (DocumentVersionState, error) {
	request.OperationID = strings.TrimSpace(request.OperationID)
	request.DocumentID = strings.TrimSpace(request.DocumentID)
	request.VersionID = strings.TrimSpace(request.VersionID)
	request.ExpectedPreviousVersionID = strings.TrimSpace(request.ExpectedPreviousVersionID)
	if request.OperationID == "" || request.DocumentID == "" || request.VersionID == "" || request.ActivationRevision == 0 {
		return DocumentVersionState{}, fmt.Errorf("%w: operation_id, document_id, version_id and activation_revision are required", ErrInvalidInput)
	}
	fingerprint := operationFingerprint(struct {
		DocumentID                string
		VersionID                 string
		ActivationRevision        uint64
		LifecycleRevision         uint64
		ExpectedPreviousVersionID string
	}{
		DocumentID:                request.DocumentID,
		VersionID:                 request.VersionID,
		ActivationRevision:        request.ActivationRevision,
		LifecycleRevision:         request.LifecycleRevision,
		ExpectedPreviousVersionID: request.ExpectedPreviousVersionID,
	})

	locked, err := s.lockWrite(ctx)
	if err != nil {
		return DocumentVersionState{}, err
	}
	defer s.unlockWrite()
	snapshot := locked
	next := snapshot.clone()
	if replay, ok, err := next.replayOperation(request.OperationID, "activate_document_version", fingerprint); err != nil {
		return DocumentVersionState{}, err
	} else if ok {
		return replay.(DocumentVersionState), nil
	}
	manifest := next.manifest(request.DocumentID)
	if err := validateLiveLifecycle(manifest, request.LifecycleRevision); err != nil {
		return DocumentVersionState{}, err
	}
	if request.ActivationRevision < manifest.activationRevision {
		return DocumentVersionState{}, fmt.Errorf("%w: current=%d requested=%d", ErrStaleActivation, manifest.activationRevision, request.ActivationRevision)
	}
	if request.ActivationRevision == manifest.activationRevision && manifest.activationRevision != 0 {
		if manifest.activationVersionID != request.VersionID {
			return DocumentVersionState{}, fmt.Errorf("%w: activation revision %d already belongs to version %q", ErrConflict, manifest.activationRevision, manifest.activationVersionID)
		}
		if manifest.tombstoned && request.LifecycleRevision > manifest.tombstoneRevision {
			version, ok := next.versionsByKey[documentVersionKey(request.DocumentID, request.VersionID)]
			if !ok {
				return DocumentVersionState{}, fmt.Errorf("%w: document version", ErrNotFound)
			}
			advanceLiveLifecycle(manifest, request.LifecycleRevision)
			manifest.activeVersionID = request.VersionID
			manifest.activationState = version.state
			manifest.tombstoned = false
			state := versionState(version, manifest)
			next.recordOperation(request.OperationID, "activate_document_version", fingerprint, state)
			if err := s.commit(ctx, snapshot, next); err != nil {
				return DocumentVersionState{}, err
			}
			return state, nil
		}
		state := next.activationRetryState(manifest)
		next.recordOperation(request.OperationID, "activate_document_version", fingerprint, state)
		if err := s.commit(ctx, snapshot, next); err != nil {
			return DocumentVersionState{}, err
		}
		return state, nil
	}

	key := documentVersionKey(request.DocumentID, request.VersionID)
	version, ok := next.versionsByKey[key]
	if !ok {
		return DocumentVersionState{}, fmt.Errorf("%w: document version", ErrNotFound)
	}
	if request.ExpectedPreviousVersionID != "" && manifest.activeVersionID != request.ExpectedPreviousVersionID {
		return DocumentVersionState{}, fmt.Errorf("%w: active version is %q, expected %q", ErrConflict, manifest.activeVersionID, request.ExpectedPreviousVersionID)
	}

	advanceLiveLifecycle(manifest, request.LifecycleRevision)
	manifest.activationRevision = request.ActivationRevision
	manifest.activationVersionID = request.VersionID
	manifest.activeVersionID = request.VersionID
	manifest.activationState = version.state
	manifest.tombstoned = false
	state := versionState(version, manifest)
	next.recordOperation(request.OperationID, "activate_document_version", fingerprint, state)
	if err := s.commit(ctx, snapshot, next); err != nil {
		return DocumentVersionState{}, err
	}
	return state, nil
}

func (s *DocumentIndexService) UpdateDocumentAccess(
	ctx context.Context,
	request UpdateDocumentAccessRequest,
) (DocumentAccessState, error) {
	request.OperationID = strings.TrimSpace(request.OperationID)
	request.DocumentID = strings.TrimSpace(request.DocumentID)
	request.GrantedSpaceIDs = normalizeStrings(request.GrantedSpaceIDs)
	if request.OperationID == "" || request.DocumentID == "" || request.AccessRevision == 0 {
		return DocumentAccessState{}, fmt.Errorf("%w: operation_id, document_id and access_revision are required", ErrInvalidInput)
	}
	fingerprint := operationFingerprint(struct {
		DocumentID          string
		AccessRevision      uint64
		LifecycleRevision   uint64
		AuthenticatedPublic bool
		GrantedSpaceIDs     []string
	}{
		DocumentID:          request.DocumentID,
		AccessRevision:      request.AccessRevision,
		LifecycleRevision:   request.LifecycleRevision,
		AuthenticatedPublic: request.AuthenticatedPublic,
		GrantedSpaceIDs:     request.GrantedSpaceIDs,
	})

	locked, err := s.lockWrite(ctx)
	if err != nil {
		return DocumentAccessState{}, err
	}
	defer s.unlockWrite()
	snapshot := locked
	next := snapshot.clone()
	if replay, ok, err := next.replayOperation(request.OperationID, "update_document_access", fingerprint); err != nil {
		return DocumentAccessState{}, err
	} else if ok {
		return cloneDocumentAccessState(replay.(DocumentAccessState)), nil
	}
	manifest := next.manifest(request.DocumentID)
	if err := validateLiveLifecycle(manifest, request.LifecycleRevision); err != nil {
		return DocumentAccessState{}, err
	}
	if request.AccessRevision < manifest.accessRevision {
		return DocumentAccessState{}, fmt.Errorf("%w: current=%d requested=%d", ErrStaleAccess, manifest.accessRevision, request.AccessRevision)
	}
	if request.AccessRevision == manifest.accessRevision {
		if request.AuthenticatedPublic != manifest.authenticatedPublic || !equalStrings(request.GrantedSpaceIDs, manifest.grantedSpaceIDs) {
			return DocumentAccessState{}, fmt.Errorf("%w: access revision %d has a different policy snapshot", ErrConflict, manifest.accessRevision)
		}
		advanceLiveLifecycle(manifest, request.LifecycleRevision)
		state := accessState(request.DocumentID, manifest)
		next.recordOperation(request.OperationID, "update_document_access", fingerprint, state)
		if err := s.commit(ctx, snapshot, next); err != nil {
			return DocumentAccessState{}, err
		}
		return state, nil
	}

	manifest.accessRevision = request.AccessRevision
	manifest.authenticatedPublic = request.AuthenticatedPublic
	manifest.grantedSpaceIDs = cloneStrings(request.GrantedSpaceIDs)
	advanceLiveLifecycle(manifest, request.LifecycleRevision)
	state := accessState(request.DocumentID, manifest)
	next.recordOperation(request.OperationID, "update_document_access", fingerprint, state)
	if err := s.commit(ctx, snapshot, next); err != nil {
		return DocumentAccessState{}, err
	}
	return state, nil
}

func (s *DocumentIndexService) DeleteDocumentVersion(
	ctx context.Context,
	request DeleteDocumentVersionRequest,
) (DeleteDocumentVersionResult, error) {
	request.OperationID = strings.TrimSpace(request.OperationID)
	request.DocumentID = strings.TrimSpace(request.DocumentID)
	request.VersionID = strings.TrimSpace(request.VersionID)
	if request.OperationID == "" || request.DocumentID == "" || request.VersionID == "" {
		return DeleteDocumentVersionResult{}, fmt.Errorf("%w: operation_id, document_id and version_id are required", ErrInvalidInput)
	}
	fingerprint := operationFingerprint(struct {
		DocumentID        string
		VersionID         string
		LifecycleRevision uint64
	}{
		DocumentID:        request.DocumentID,
		VersionID:         request.VersionID,
		LifecycleRevision: request.LifecycleRevision,
	})

	locked, err := s.lockWrite(ctx)
	if err != nil {
		return DeleteDocumentVersionResult{}, err
	}
	defer s.unlockWrite()
	snapshot := locked
	next := snapshot.clone()
	if replay, ok, err := next.replayOperation(request.OperationID, "delete_document_version", fingerprint); err != nil {
		return DeleteDocumentVersionResult{}, err
	} else if ok {
		return replay.(DeleteDocumentVersionResult), nil
	}
	manifest := next.manifest(request.DocumentID)
	if byRevision := manifest.versionDeleteResults[request.VersionID]; byRevision != nil {
		if result, ok := byRevision[request.LifecycleRevision]; ok {
			next.recordOperation(request.OperationID, "delete_document_version", fingerprint, result)
			if err := s.commit(ctx, snapshot, next); err != nil {
				return DeleteDocumentVersionResult{}, err
			}
			return result, nil
		}
	}
	if err := validateLiveLifecycle(manifest, request.LifecycleRevision); err != nil {
		return DeleteDocumentVersionResult{}, err
	}

	key := documentVersionKey(request.DocumentID, request.VersionID)
	version, ok := next.versionsByKey[key]
	if !ok {
		advanceLiveLifecycle(manifest, request.LifecycleRevision)
		result := DeleteDocumentVersionResult{}
		recordVersionDelete(manifest, request.VersionID, request.LifecycleRevision, result)
		next.recordOperation(request.OperationID, "delete_document_version", fingerprint, result)
		if err := s.commit(ctx, snapshot, next); err != nil {
			return DeleteDocumentVersionResult{}, err
		}
		return result, nil
	}
	delete(next.versionsByKey, key)
	delete(next.versionsByStore, version.storageID)
	next.pendingVectorDeletes[version.storageID] = struct{}{}
	activeRemoved := manifest.activeVersionID == request.VersionID
	if activeRemoved {
		manifest.activeVersionID = ""
	}
	advanceLiveLifecycle(manifest, request.LifecycleRevision)
	result := DeleteDocumentVersionResult{Deleted: true, ActiveVersionRemoved: activeRemoved}
	recordVersionDelete(manifest, request.VersionID, request.LifecycleRevision, result)
	next.recordOperation(request.OperationID, "delete_document_version", fingerprint, result)
	if err := s.commit(ctx, snapshot, next); err != nil {
		return DeleteDocumentVersionResult{}, err
	}
	if err := s.reconcilePendingVectorDeletes(ctx, next); err != nil {
		return DeleteDocumentVersionResult{}, err
	}
	return result, nil
}

func (s *DocumentIndexService) DeleteDocument(
	ctx context.Context,
	request DeleteDocumentRequest,
) (DeleteDocumentResult, error) {
	request.OperationID = strings.TrimSpace(request.OperationID)
	request.DocumentID = strings.TrimSpace(request.DocumentID)
	if request.OperationID == "" || request.DocumentID == "" || request.LifecycleRevision == 0 {
		return DeleteDocumentResult{}, fmt.Errorf("%w: operation_id, document_id and lifecycle_revision are required", ErrInvalidInput)
	}
	fingerprint := operationFingerprint(struct {
		DocumentID        string
		LifecycleRevision uint64
	}{
		DocumentID:        request.DocumentID,
		LifecycleRevision: request.LifecycleRevision,
	})

	locked, err := s.lockWrite(ctx)
	if err != nil {
		return DeleteDocumentResult{}, err
	}
	defer s.unlockWrite()
	snapshot := locked
	next := snapshot.clone()
	if replay, ok, err := next.replayOperation(request.OperationID, "delete_document", fingerprint); err != nil {
		return DeleteDocumentResult{}, err
	} else if ok {
		return replay.(DeleteDocumentResult), nil
	}
	manifest := next.manifest(request.DocumentID)
	if result, ok := manifest.documentDeleteResults[request.LifecycleRevision]; ok {
		next.recordOperation(request.OperationID, "delete_document", fingerprint, result)
		if err := s.commit(ctx, snapshot, next); err != nil {
			return DeleteDocumentResult{}, err
		}
		return result, nil
	}
	if request.LifecycleRevision < manifest.lifecycleRevision {
		return DeleteDocumentResult{}, fmt.Errorf("%w: current=%d requested=%d", ErrStaleLifecycle, manifest.lifecycleRevision, request.LifecycleRevision)
	}
	if request.LifecycleRevision == manifest.lifecycleRevision {
		return DeleteDocumentResult{}, fmt.Errorf("%w: lifecycle revision %d already represents a live document generation", ErrConflict, request.LifecycleRevision)
	}

	for key, version := range next.versionsByKey {
		if version.state.DocumentID == request.DocumentID {
			delete(next.versionsByStore, version.storageID)
			delete(next.versionsByKey, key)
			next.pendingVectorDeletes[version.storageID] = struct{}{}
		}
	}

	manifest.activeVersionID = ""
	manifest.lifecycleRevision = request.LifecycleRevision
	manifest.tombstoneRevision = request.LifecycleRevision
	manifest.tombstoned = true
	result := DeleteDocumentResult{Tombstoned: true, LifecycleRevision: request.LifecycleRevision}
	manifest.documentDeleteResults[request.LifecycleRevision] = result
	next.recordOperation(request.OperationID, "delete_document", fingerprint, result)
	if err := s.commit(ctx, snapshot, next); err != nil {
		return DeleteDocumentResult{}, err
	}
	if err := s.reconcilePendingVectorDeletes(ctx, next); err != nil {
		return DeleteDocumentResult{}, err
	}
	return result, nil
}

func (s *DocumentIndexService) GetDocumentVersionState(
	ctx context.Context,
	request GetDocumentVersionStateRequest,
) (GetDocumentVersionStateResult, error) {
	request.DocumentID = strings.TrimSpace(request.DocumentID)
	request.VersionID = strings.TrimSpace(request.VersionID)
	if request.DocumentID == "" || request.VersionID == "" {
		return GetDocumentVersionStateResult{}, fmt.Errorf("%w: document_id and version_id are required", ErrInvalidInput)
	}

	snapshot, err := s.readSnapshot(ctx)
	if err != nil {
		return GetDocumentVersionStateResult{}, err
	}
	version, ok := snapshot.versionsByKey[documentVersionKey(request.DocumentID, request.VersionID)]
	if !ok {
		return GetDocumentVersionStateResult{}, nil
	}
	return GetDocumentVersionStateResult{Exists: true, State: versionState(version, snapshot.manifests[request.DocumentID])}, nil
}

func (s *DocumentIndexService) SearchDocuments(
	ctx context.Context,
	request SearchDocumentsRequest,
) (SearchDocumentsResult, error) {
	request.Query = strings.TrimSpace(request.Query)
	if request.Query == "" {
		return SearchDocumentsResult{}, fmt.Errorf("%w: query is required", ErrInvalidInput)
	}
	if request.TopK < 0 || request.TopK > 100 {
		return SearchDocumentsResult{}, fmt.Errorf("%w: top_k must be between 0 and 100", ErrInvalidInput)
	}
	if request.TopK == 0 {
		request.TopK = 3
	}
	allowedSpaces := stringSet(request.AllowedSpaceIDs)
	allowedDocuments := stringSet(request.AllowedDocumentIDs)
	filter := VectorSearchFilter{
		StorageDomain:      s.storageDomain,
		AllowedSpaceIDs:    normalizeStrings(request.AllowedSpaceIDs),
		AllowedDocumentIDs: normalizeStrings(request.AllowedDocumentIDs),
	}
	candidateLimit := max(request.TopK*2, 16)
	maxCandidateLimit := min(max(request.TopK*16, 128), 800)

	snapshot, err := s.readSnapshot(ctx)
	if err != nil {
		return SearchDocumentsResult{}, err
	}
	// The vector-store projection is converged by the background reconciler. A
	// request pays for it only when the reconciler has not caught up yet, so a
	// steady-state search performs no projection write, holds no lock, and never
	// touches the control store beyond the generation probe above.
	if err := s.ensureProjection(ctx, snapshot); err != nil {
		return SearchDocumentsResult{}, err
	}

	for {
		raw, err := s.core.Search(ctx, SearchRequest{Query: request.Query, TopK: candidateLimit, Filter: &filter})
		if err != nil {
			return SearchDocumentsResult{}, err
		}

		hits := make([]SearchHit, 0, request.TopK)
		seen := make(map[string]struct{}, request.TopK)
		for _, hit := range raw.Hits {
			key, ok := snapshot.versionsByStore[hit.Chunk.DocumentID]
			if !ok {
				continue
			}
			version, ok := snapshot.versionsByKey[key]
			if !ok {
				continue
			}
			manifest := snapshot.manifests[version.state.DocumentID]
			if manifest == nil || manifest.tombstoned || manifest.activeVersionID != version.state.VersionID {
				continue
			}
			if !authorizedDocument(version, manifest, allowedSpaces, allowedDocuments) {
				continue
			}

			hit.Chunk.ID = fmt.Sprintf("%s@%s#%03d", version.state.DocumentID, version.state.VersionID, hit.Chunk.Position)
			if _, duplicate := seen[hit.Chunk.ID]; duplicate {
				continue
			}
			seen[hit.Chunk.ID] = struct{}{}
			hit.Chunk.DocumentID = version.state.DocumentID
			hit.Chunk.VersionID = version.state.VersionID
			hit.Chunk.OwnerSpaceID = version.state.OwnerSpaceID
			hit.Chunk.Source = version.sourceURI
			hit.Chunk.ContentSHA256 = version.state.ContentSHA256
			hit.Chunk.Metadata = cloneStringMap(version.metadata)
			hits = append(hits, hit)
			if len(hits) == request.TopK {
				break
			}
		}

		exhausted := len(raw.Hits) < candidateLimit
		if len(hits) == request.TopK || exhausted || candidateLimit >= maxCandidateLimit {
			return SearchDocumentsResult{
				Query:     raw.Query,
				Hits:      hits,
				Truncated: len(hits) < request.TopK && !exhausted,
			}, nil
		}
		candidateLimit = min(candidateLimit*2, maxCandidateLimit)
	}
}

func validateLiveLifecycle(manifest *documentManifest, revision uint64) error {
	if revision < manifest.lifecycleRevision {
		return fmt.Errorf("%w: current=%d requested=%d", ErrStaleLifecycle, manifest.lifecycleRevision, revision)
	}
	if manifest.tombstoned && revision <= manifest.tombstoneRevision {
		return fmt.Errorf("%w: document is tombstoned at revision %d", ErrStaleLifecycle, manifest.tombstoneRevision)
	}
	return nil
}

func advanceLiveLifecycle(manifest *documentManifest, revision uint64) {
	if revision > manifest.lifecycleRevision {
		manifest.lifecycleRevision = revision
	}
}

func versionState(version contractVersion, manifest *documentManifest) DocumentVersionState {
	state := version.state
	if manifest == nil {
		return state
	}
	state.AccessRevision = manifest.accessRevision
	state.LifecycleRevision = manifest.lifecycleRevision
	state.Status = DocumentVersionIndexed
	state.ActivationRevision = 0
	if !manifest.tombstoned && manifest.activeVersionID == state.VersionID {
		state.Status = DocumentVersionActive
		state.ActivationRevision = manifest.activationRevision
	}
	return state
}

func accessState(documentID string, manifest *documentManifest) DocumentAccessState {
	return DocumentAccessState{
		DocumentID:          documentID,
		AccessRevision:      manifest.accessRevision,
		LifecycleRevision:   manifest.lifecycleRevision,
		AuthenticatedPublic: manifest.authenticatedPublic,
		GrantedSpaceIDs:     cloneStrings(manifest.grantedSpaceIDs),
	}
}

func cloneDocumentAccessState(state DocumentAccessState) DocumentAccessState {
	state.GrantedSpaceIDs = cloneStrings(state.GrantedSpaceIDs)
	return state
}

func cloneOperationResult(result any) any {
	if state, ok := result.(DocumentAccessState); ok {
		return cloneDocumentAccessState(state)
	}
	return result
}

func authorizedDocument(
	version contractVersion,
	manifest *documentManifest,
	allowedSpaces map[string]struct{},
	allowedDocuments map[string]struct{},
) bool {
	if manifest.authenticatedPublic {
		return true
	}
	if _, ok := allowedDocuments[version.state.DocumentID]; ok {
		return true
	}
	if _, ok := allowedSpaces[manifest.ownerSpaceID]; ok {
		return true
	}
	for _, spaceID := range manifest.grantedSpaceIDs {
		if _, ok := allowedSpaces[spaceID]; ok {
			return true
		}
	}
	return false
}

func recordVersionDelete(
	manifest *documentManifest,
	versionID string,
	revision uint64,
	result DeleteDocumentVersionResult,
) {
	byRevision := manifest.versionDeleteResults[versionID]
	if byRevision == nil {
		byRevision = make(map[uint64]DeleteDocumentVersionResult)
		manifest.versionDeleteResults[versionID] = byRevision
	}
	byRevision[revision] = result
}

func documentVersionKey(documentID, versionID string) string {
	return fmt.Sprintf("%d:%s%s", len(documentID), documentID, versionID)
}

func storageDocumentID(storageDomain, documentID, versionID, operationID string) string {
	sum := sha256.Sum256([]byte(storageDomain + "\x00" + documentVersionKey(documentID, versionID) + "\x00" + operationID))
	return "dv-" + hex.EncodeToString(sum[:])
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result[value] = struct{}{}
		}
	}
	return result
}

func normalizeStrings(values []string) []string {
	set := stringSet(values)
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
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

func cloneStrings(input []string) []string {
	if len(input) == 0 {
		return nil
	}
	return append([]string(nil), input...)
}

func cloneStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func operationFingerprint(payload any) string {
	encoded, err := json.Marshal(payload)
	if err != nil {
		panic(fmt.Sprintf("marshal operation fingerprint: %v", err))
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
