package rag

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestSearchDocumentsPushesNormalizedControlsAndAuthorization(t *testing.T) {
	store := &recordingControlledStore{MemoryStore: NewMemoryStore()}
	core, err := NewServiceWithStore(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })
	service, err := NewDocumentIndexService(core)
	if err != nil {
		t.Fatal(err)
	}

	mustIndexContractVersion(t, service, "controlled-document", "v1", "owner-space", 0, "controlledneedle")
	mustActivateContractVersion(t, service, "controlled-document", "v1", 7, 0)
	if _, err := service.UpdateDocumentAccess(context.Background(), UpdateDocumentAccessRequest{
		OperationID: "controlled-access", DocumentID: "controlled-document", AccessRevision: 9,
		GrantedSpaceIDs: []string{" shared-space ", "shared-space"},
	}); err != nil {
		t.Fatal(err)
	}

	result, err := service.SearchDocuments(context.Background(), SearchDocumentsRequest{
		Query: "controlledneedle", AllowedSpaceIDs: []string{" shared-space ", "shared-space"}, TopK: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Hits) != 1 {
		t.Fatalf("hits = %d, want 1", len(result.Hits))
	}
	controls, filters, denseCalls, sparseCalls := store.snapshotCalls()
	if len(controls) != 1 {
		t.Fatalf("controls = %+v, want one projection", controls)
	}
	control := controls[0]
	if control.StorageDomain == "" || control.DocumentID != "controlled-document" || control.VersionID != "v1" ||
		control.OwnerSpaceID != "owner-space" || !control.Active || control.Tombstoned ||
		control.ActivationRevision != 7 || control.AccessRevision != 9 ||
		len(control.GrantedSpaceIDs) != 1 || control.GrantedSpaceIDs[0] != "shared-space" {
		t.Fatalf("unexpected control projection: %+v", control)
	}
	if denseCalls == 0 || sparseCalls == 0 || len(filters) == 0 {
		t.Fatalf("filtered recall calls dense=%d sparse=%d filters=%+v", denseCalls, sparseCalls, filters)
	}
	filter := filters[len(filters)-1]
	if filter.StorageDomain != control.StorageDomain || len(filter.AllowedSpaceIDs) != 1 || filter.AllowedSpaceIDs[0] != "shared-space" {
		t.Fatalf("normalized filter = %+v", filter)
	}
}

func TestSearchDocumentsRefillsAfterContractFiltering(t *testing.T) {
	store := newStarvingStore()
	core, err := NewServiceWithStore(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })
	service, err := NewDocumentIndexService(core)
	if err != nil {
		t.Fatal(err)
	}

	for index := 0; index < 20; index++ {
		documentID := "inactive-" + string(rune('a'+index))
		mustIndexContractVersion(t, service, documentID, "v1", "private-space", 0, "refillneedle")
	}
	mustIndexContractVersion(t, service, "public-target", "v1", "owner-space", 0, "refillneedle")
	mustActivateContractVersion(t, service, "public-target", "v1", 1, 0)
	if _, err := service.UpdateDocumentAccess(context.Background(), UpdateDocumentAccessRequest{
		OperationID: "public-target-access", DocumentID: "public-target", AccessRevision: 1,
		AuthenticatedPublic: true,
	}); err != nil {
		t.Fatal(err)
	}

	result, err := service.SearchDocuments(context.Background(), SearchDocumentsRequest{Query: "refillneedle", TopK: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Hits) != 1 || result.Hits[0].Chunk.DocumentID != "public-target" {
		t.Fatalf("refill result = %+v, want public target", result)
	}
	if calls := store.searchCallCount(); calls < 4 {
		t.Fatalf("dense+sparse calls = %d, want at least two recall rounds", calls)
	}
}

func TestSearchDocumentsFailsClosedWhenControlProjectionFails(t *testing.T) {
	wantErr := errors.New("control projection unavailable")
	store := &recordingControlledStore{MemoryStore: NewMemoryStore(), syncErr: wantErr}
	core, err := NewServiceWithStore(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })
	service, err := NewDocumentIndexService(core)
	if err != nil {
		t.Fatal(err)
	}
	mustIndexContractVersion(t, service, "fail-closed", "v1", "owner", 0, "failureneedle")
	mustActivateContractVersion(t, service, "fail-closed", "v1", 1, 0)
	if _, err := service.SearchDocuments(context.Background(), SearchDocumentsRequest{
		Query: "failureneedle", AllowedSpaceIDs: []string{"owner"}, TopK: 1,
	}); !errors.Is(err, wantErr) {
		t.Fatalf("search error = %v, want projection failure", err)
	}
	_, _, denseCalls, sparseCalls := store.snapshotCalls()
	if denseCalls != 0 || sparseCalls != 0 {
		t.Fatalf("recall ran after projection failure: dense=%d sparse=%d", denseCalls, sparseCalls)
	}
}

func TestQdrantControlFilterAuthorizationShape(t *testing.T) {
	publicOnly := qdrantControlFilter(VectorSearchFilter{})
	if len(publicOnly.Must) != 2 || publicOnly.MinShould == nil ||
		publicOnly.MinShould.MinCount != 1 || len(publicOnly.MinShould.Conditions) != 1 {
		t.Fatalf("public-only filter = %+v", publicOnly)
	}
	full := qdrantControlFilter(VectorSearchFilter{
		StorageDomain:      "control-domain",
		AllowedSpaceIDs:    []string{"space-b", " space-a ", "space-a"},
		AllowedDocumentIDs: []string{"document-a", "document-a"},
	})
	if len(full.Must) != 3 || full.MinShould == nil ||
		full.MinShould.MinCount != 1 || len(full.MinShould.Conditions) != 4 {
		t.Fatalf("full authorization filter = %+v", full)
	}
}

type recordingControlledStore struct {
	*MemoryStore

	mu          sync.Mutex
	controls    []VectorDocumentControl
	filters     []VectorSearchFilter
	denseCalls  int
	sparseCalls int
	syncCalls   int
	syncErr     error
}

func (s *recordingControlledStore) SyncDocumentControls(_ context.Context, controls []VectorDocumentControl) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.syncCalls++
	s.controls = append([]VectorDocumentControl(nil), controls...)
	for index := range s.controls {
		s.controls[index].GrantedSpaceIDs = cloneStrings(s.controls[index].GrantedSpaceIDs)
	}
	return s.syncErr
}

// projectionCalls reports how many times the vector store received a control
// projection, which is how the read path proves it performed no projection
// write of its own.
func (s *recordingControlledStore) projectionCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.syncCalls
}

func (s *recordingControlledStore) DenseSearchFiltered(
	ctx context.Context,
	query []float64,
	limit int,
	filter VectorSearchFilter,
) ([]ScoredChunk, error) {
	s.recordFilter(filter, true)
	return s.MemoryStore.DenseSearch(ctx, query, limit)
}

func (s *recordingControlledStore) SparseSearchFiltered(
	ctx context.Context,
	queryTokens []string,
	limit int,
	filter VectorSearchFilter,
) ([]ScoredChunk, error) {
	s.recordFilter(filter, false)
	return s.MemoryStore.SparseSearch(ctx, queryTokens, limit)
}

func (s *recordingControlledStore) recordFilter(filter VectorSearchFilter, dense bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	filter.AllowedSpaceIDs = cloneStrings(filter.AllowedSpaceIDs)
	filter.AllowedDocumentIDs = cloneStrings(filter.AllowedDocumentIDs)
	s.filters = append(s.filters, filter)
	if dense {
		s.denseCalls++
	} else {
		s.sparseCalls++
	}
}

func (s *recordingControlledStore) snapshotCalls() ([]VectorDocumentControl, []VectorSearchFilter, int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]VectorDocumentControl(nil), s.controls...), append([]VectorSearchFilter(nil), s.filters...), s.denseCalls, s.sparseCalls
}

type starvingStore struct {
	mu          sync.Mutex
	chunks      map[string]IndexedChunk
	order       []string
	searchCalls int
}

func newStarvingStore() *starvingStore {
	return &starvingStore{chunks: make(map[string]IndexedChunk)}
}

func (s *starvingStore) ReplaceDocument(_ context.Context, documentID string, chunks []IndexedChunk) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for chunkID, chunk := range s.chunks {
		if chunk.Chunk.DocumentID == documentID {
			delete(s.chunks, chunkID)
		}
	}
	if len(chunks) == 0 {
		return nil
	}
	for _, chunk := range chunks {
		if _, exists := s.chunks[chunk.Chunk.ID]; !exists {
			s.order = append(s.order, chunk.Chunk.ID)
		}
		s.chunks[chunk.Chunk.ID] = chunk
	}
	return nil
}

func (s *starvingStore) DenseSearch(_ context.Context, _ []float64, limit int) ([]ScoredChunk, error) {
	return s.search(limit), nil
}

func (s *starvingStore) SparseSearch(_ context.Context, _ []string, limit int) ([]ScoredChunk, error) {
	return s.search(limit), nil
}

func (s *starvingStore) search(limit int) []ScoredChunk {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.searchCalls++
	ids := append([]string(nil), s.order...)
	if limit > 0 && len(ids) > limit {
		ids = ids[:limit]
	}
	result := make([]ScoredChunk, 0, len(ids))
	for index, id := range ids {
		result = append(result, ScoredChunk{Chunk: s.chunks[id].Chunk, Score: float64(len(ids) - index)})
	}
	return result
}

func (s *starvingStore) searchCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.searchCalls
}

func (s *starvingStore) Close() error { return nil }
