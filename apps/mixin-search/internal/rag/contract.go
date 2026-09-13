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
// Eino workflows. The manifest remains process-local in P1.5; losing it fails
// closed because a restarted process has no active document versions.
type DocumentIndexService struct {
	core *Service

	mu                  sync.RWMutex
	versionsByKey       map[string]contractVersion
	versionsByStore     map[string]string
	versionFingerprints map[string]versionFingerprint
	manifests           map[string]*documentManifest
	operations          map[string]operationRecord
}

func NewDocumentIndexService(core *Service) (*DocumentIndexService, error) {
	if core == nil {
		return nil, errors.New("rag core service is required")
	}
	return &DocumentIndexService{
		core:                core,
		versionsByKey:       make(map[string]contractVersion),
		versionsByStore:     make(map[string]string),
		versionFingerprints: make(map[string]versionFingerprint),
		manifests:           make(map[string]*documentManifest),
		operations:          make(map[string]operationRecord),
	}, nil
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

	s.mu.Lock()
	defer s.mu.Unlock()
	if replay, ok, err := s.replayOperationLocked(request.OperationID, "index_document_version", fingerprint); err != nil {
		return DocumentVersionState{}, err
	} else if ok {
		return replay.(DocumentVersionState), nil
	}
	manifest := s.manifestLocked(request.DocumentID)
	if err := validateLiveLifecycle(manifest, request.LifecycleRevision); err != nil {
		return DocumentVersionState{}, err
	}
	if manifest.ownerSpaceID != "" && manifest.ownerSpaceID != request.OwnerSpaceID {
		return DocumentVersionState{}, fmt.Errorf("%w: document owner space is %q, requested %q", ErrConflict, manifest.ownerSpaceID, request.OwnerSpaceID)
	}

	key := documentVersionKey(request.DocumentID, request.VersionID)
	if fingerprint, ok := s.versionFingerprints[key]; ok &&
		(fingerprint.contentSHA256 != digest || fingerprint.ownerSpaceID != request.OwnerSpaceID) {
		return DocumentVersionState{}, fmt.Errorf("%w: document version already exists with different content or owner space", ErrConflict)
	}
	if existing, ok := s.versionsByKey[key]; ok {
		advanceLiveLifecycle(manifest, request.LifecycleRevision)
		state := versionState(existing, manifest)
		s.recordOperationLocked(request.OperationID, "index_document_version", fingerprint, state)
		return state, nil
	}

	storageID := storageDocumentID(request.DocumentID, request.VersionID)
	result, err := s.core.IngestDocument(ctx, IngestDocumentRequest{
		DocumentID: storageID,
		Filename:   request.Filename,
		Title:      request.Title,
		Content:    request.Content,
		ChunkSize:  request.ChunkSize,
		Overlap:    request.Overlap,
	})
	if err != nil {
		return DocumentVersionState{}, err
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
	s.versionsByKey[key] = version
	s.versionsByStore[storageID] = key
	s.versionFingerprints[key] = versionFingerprint{contentSHA256: digest, ownerSpaceID: request.OwnerSpaceID}
	if manifest.ownerSpaceID == "" {
		manifest.ownerSpaceID = request.OwnerSpaceID
	}
	advanceLiveLifecycle(manifest, request.LifecycleRevision)
	state := versionState(version, manifest)
	s.recordOperationLocked(request.OperationID, "index_document_version", fingerprint, state)
	return state, nil
}

func (s *DocumentIndexService) ActivateDocumentVersion(
	_ context.Context,
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

	s.mu.Lock()
	defer s.mu.Unlock()
	if replay, ok, err := s.replayOperationLocked(request.OperationID, "activate_document_version", fingerprint); err != nil {
		return DocumentVersionState{}, err
	} else if ok {
		return replay.(DocumentVersionState), nil
	}
	manifest := s.manifestLocked(request.DocumentID)
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
			version, ok := s.versionsByKey[documentVersionKey(request.DocumentID, request.VersionID)]
			if !ok {
				return DocumentVersionState{}, fmt.Errorf("%w: document version", ErrNotFound)
			}
			advanceLiveLifecycle(manifest, request.LifecycleRevision)
			manifest.activeVersionID = request.VersionID
			manifest.activationState = version.state
			manifest.tombstoned = false
			state := versionState(version, manifest)
			s.recordOperationLocked(request.OperationID, "activate_document_version", fingerprint, state)
			return state, nil
		}
		state := s.activationRetryStateLocked(manifest)
		s.recordOperationLocked(request.OperationID, "activate_document_version", fingerprint, state)
		return state, nil
	}

	key := documentVersionKey(request.DocumentID, request.VersionID)
	version, ok := s.versionsByKey[key]
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
	s.recordOperationLocked(request.OperationID, "activate_document_version", fingerprint, state)
	return state, nil
}

func (s *DocumentIndexService) UpdateDocumentAccess(
	_ context.Context,
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

	s.mu.Lock()
	defer s.mu.Unlock()
	if replay, ok, err := s.replayOperationLocked(request.OperationID, "update_document_access", fingerprint); err != nil {
		return DocumentAccessState{}, err
	} else if ok {
		return cloneDocumentAccessState(replay.(DocumentAccessState)), nil
	}
	manifest := s.manifestLocked(request.DocumentID)
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
		s.recordOperationLocked(request.OperationID, "update_document_access", fingerprint, state)
		return state, nil
	}

	manifest.accessRevision = request.AccessRevision
	manifest.authenticatedPublic = request.AuthenticatedPublic
	manifest.grantedSpaceIDs = cloneStrings(request.GrantedSpaceIDs)
	advanceLiveLifecycle(manifest, request.LifecycleRevision)
	state := accessState(request.DocumentID, manifest)
	s.recordOperationLocked(request.OperationID, "update_document_access", fingerprint, state)
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

	s.mu.Lock()
	defer s.mu.Unlock()
	if replay, ok, err := s.replayOperationLocked(request.OperationID, "delete_document_version", fingerprint); err != nil {
		return DeleteDocumentVersionResult{}, err
	} else if ok {
		return replay.(DeleteDocumentVersionResult), nil
	}
	manifest := s.manifestLocked(request.DocumentID)
	if byRevision := manifest.versionDeleteResults[request.VersionID]; byRevision != nil {
		if result, ok := byRevision[request.LifecycleRevision]; ok {
			s.recordOperationLocked(request.OperationID, "delete_document_version", fingerprint, result)
			return result, nil
		}
	}
	if err := validateLiveLifecycle(manifest, request.LifecycleRevision); err != nil {
		return DeleteDocumentVersionResult{}, err
	}

	key := documentVersionKey(request.DocumentID, request.VersionID)
	version, ok := s.versionsByKey[key]
	if !ok {
		advanceLiveLifecycle(manifest, request.LifecycleRevision)
		result := DeleteDocumentVersionResult{}
		recordVersionDelete(manifest, request.VersionID, request.LifecycleRevision, result)
		s.recordOperationLocked(request.OperationID, "delete_document_version", fingerprint, result)
		return result, nil
	}
	if err := s.core.DeleteIndexedDocument(ctx, version.storageID); err != nil {
		return DeleteDocumentVersionResult{}, err
	}

	delete(s.versionsByKey, key)
	delete(s.versionsByStore, version.storageID)
	activeRemoved := manifest.activeVersionID == request.VersionID
	if activeRemoved {
		manifest.activeVersionID = ""
	}
	advanceLiveLifecycle(manifest, request.LifecycleRevision)
	result := DeleteDocumentVersionResult{Deleted: true, ActiveVersionRemoved: activeRemoved}
	recordVersionDelete(manifest, request.VersionID, request.LifecycleRevision, result)
	s.recordOperationLocked(request.OperationID, "delete_document_version", fingerprint, result)
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

	s.mu.Lock()
	defer s.mu.Unlock()
	if replay, ok, err := s.replayOperationLocked(request.OperationID, "delete_document", fingerprint); err != nil {
		return DeleteDocumentResult{}, err
	} else if ok {
		return replay.(DeleteDocumentResult), nil
	}
	manifest := s.manifestLocked(request.DocumentID)
	if result, ok := manifest.documentDeleteResults[request.LifecycleRevision]; ok {
		s.recordOperationLocked(request.OperationID, "delete_document", fingerprint, result)
		return result, nil
	}
	if request.LifecycleRevision < manifest.lifecycleRevision {
		return DeleteDocumentResult{}, fmt.Errorf("%w: current=%d requested=%d", ErrStaleLifecycle, manifest.lifecycleRevision, request.LifecycleRevision)
	}
	if request.LifecycleRevision == manifest.lifecycleRevision {
		return DeleteDocumentResult{}, fmt.Errorf("%w: lifecycle revision %d already represents a live document generation", ErrConflict, request.LifecycleRevision)
	}

	versions := make([]contractVersion, 0)
	for _, version := range s.versionsByKey {
		if version.state.DocumentID == request.DocumentID {
			versions = append(versions, version)
		}
	}
	for _, version := range versions {
		if err := s.core.DeleteIndexedDocument(ctx, version.storageID); err != nil {
			return DeleteDocumentResult{}, err
		}
	}
	for key, version := range s.versionsByKey {
		if version.state.DocumentID == request.DocumentID {
			delete(s.versionsByStore, version.storageID)
			delete(s.versionsByKey, key)
		}
	}

	manifest.activeVersionID = ""
	manifest.lifecycleRevision = request.LifecycleRevision
	manifest.tombstoneRevision = request.LifecycleRevision
	manifest.tombstoned = true
	result := DeleteDocumentResult{Tombstoned: true, LifecycleRevision: request.LifecycleRevision}
	manifest.documentDeleteResults[request.LifecycleRevision] = result
	s.recordOperationLocked(request.OperationID, "delete_document", fingerprint, result)
	return result, nil
}

func (s *DocumentIndexService) GetDocumentVersionState(
	_ context.Context,
	request GetDocumentVersionStateRequest,
) (GetDocumentVersionStateResult, error) {
	request.DocumentID = strings.TrimSpace(request.DocumentID)
	request.VersionID = strings.TrimSpace(request.VersionID)
	if request.DocumentID == "" || request.VersionID == "" {
		return GetDocumentVersionStateResult{}, fmt.Errorf("%w: document_id and version_id are required", ErrInvalidInput)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	version, ok := s.versionsByKey[documentVersionKey(request.DocumentID, request.VersionID)]
	if !ok {
		return GetDocumentVersionStateResult{}, nil
	}
	return GetDocumentVersionStateResult{Exists: true, State: versionState(version, s.manifests[request.DocumentID])}, nil
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
	candidateLimit := max(request.TopK*8, 32)

	s.mu.RLock()
	defer s.mu.RUnlock()
	raw, err := s.core.Search(ctx, SearchRequest{Query: request.Query, TopK: candidateLimit})
	if err != nil {
		return SearchDocumentsResult{}, err
	}

	hits := make([]SearchHit, 0, request.TopK)
	for _, hit := range raw.Hits {
		key, ok := s.versionsByStore[hit.Chunk.DocumentID]
		if !ok {
			continue
		}
		version, ok := s.versionsByKey[key]
		if !ok {
			continue
		}
		manifest := s.manifests[version.state.DocumentID]
		if manifest == nil || manifest.tombstoned || manifest.activeVersionID != version.state.VersionID {
			continue
		}
		if !authorizedDocument(version, manifest, allowedSpaces, allowedDocuments) {
			continue
		}

		hit.Chunk.ID = fmt.Sprintf("%s@%s#%03d", version.state.DocumentID, version.state.VersionID, hit.Chunk.Position)
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
	return SearchDocumentsResult{
		Query:     raw.Query,
		Hits:      hits,
		Truncated: len(raw.Hits) == candidateLimit && len(hits) < request.TopK,
	}, nil
}

func (s *DocumentIndexService) manifestLocked(documentID string) *documentManifest {
	manifest := s.manifests[documentID]
	if manifest == nil {
		manifest = &documentManifest{
			documentDeleteResults: make(map[uint64]DeleteDocumentResult),
			versionDeleteResults:  make(map[string]map[uint64]DeleteDocumentVersionResult),
		}
		s.manifests[documentID] = manifest
	}
	return manifest
}

func (s *DocumentIndexService) replayOperationLocked(
	operationID string,
	kind string,
	fingerprint string,
) (any, bool, error) {
	record, ok := s.operations[operationID]
	if !ok {
		return nil, false, nil
	}
	if record.kind != kind || record.fingerprint != fingerprint {
		return nil, false, fmt.Errorf("%w: operation_id %q is already bound to %s", ErrConflict, operationID, record.kind)
	}
	return cloneOperationResult(record.result), true, nil
}

func (s *DocumentIndexService) recordOperationLocked(
	operationID string,
	kind string,
	fingerprint string,
	result any,
) {
	s.operations[operationID] = operationRecord{
		kind:        kind,
		fingerprint: fingerprint,
		result:      cloneOperationResult(result),
	}
}

func (s *DocumentIndexService) activationRetryStateLocked(manifest *documentManifest) DocumentVersionState {
	if version, ok := s.versionsByKey[documentVersionKey(manifest.activationState.DocumentID, manifest.activationVersionID)]; ok {
		return versionState(version, manifest)
	}
	state := manifest.activationState
	state.Status = DocumentVersionIndexed
	state.ActivationRevision = 0
	state.AccessRevision = manifest.accessRevision
	state.LifecycleRevision = manifest.lifecycleRevision
	return state
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
	return documentID + "\x00" + versionID
}

func storageDocumentID(documentID, versionID string) string {
	sum := sha256.Sum256([]byte(documentVersionKey(documentID, versionID)))
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
