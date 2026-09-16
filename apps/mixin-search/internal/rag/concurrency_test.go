package rag

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// countingControlStore records how often the service loads the complete control
// plane compared with how often it only probes the generation.
type countingControlStore struct {
	inner       ControlStore
	loads       atomic.Int64
	generations atomic.Int64
}

func (s *countingControlStore) Load(ctx context.Context) (ControlState, error) {
	s.loads.Add(1)
	return s.inner.Load(ctx)
}

func (s *countingControlStore) Generation(ctx context.Context) (uint64, error) {
	s.generations.Add(1)
	return s.inner.Generation(ctx)
}

func (s *countingControlStore) Save(ctx context.Context, generation uint64, state ControlState) (uint64, error) {
	return s.inner.Save(ctx, generation, state)
}

func (s *countingControlStore) StorageDomain() string { return s.inner.StorageDomain() }

// concurrencyRecordingStore observes how many searches are inside the vector
// store at the same time. Under the previous global exclusive lock this could
// never exceed one.
type concurrencyRecordingStore struct {
	*MemoryStore

	inFlight    atomic.Int64
	maxInFlight atomic.Int64
	denseDelay  time.Duration
	mu          sync.Mutex
	syncCalls   int
}

func (s *concurrencyRecordingStore) SyncDocumentControls(_ context.Context, _ []VectorDocumentControl) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.syncCalls++
	return nil
}

func (s *concurrencyRecordingStore) DenseSearchFiltered(
	ctx context.Context,
	query []float64,
	limit int,
	_ VectorSearchFilter,
) ([]ScoredChunk, error) {
	s.enter()
	defer s.leave()
	if s.denseDelay > 0 {
		select {
		case <-time.After(s.denseDelay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return s.MemoryStore.DenseSearch(ctx, query, limit)
}

func (s *concurrencyRecordingStore) SparseSearchFiltered(
	ctx context.Context,
	queryTokens []string,
	limit int,
	_ VectorSearchFilter,
) ([]ScoredChunk, error) {
	s.enter()
	defer s.leave()
	return s.MemoryStore.SparseSearch(ctx, queryTokens, limit)
}

func (s *concurrencyRecordingStore) enter() {
	current := s.inFlight.Add(1)
	for {
		observed := s.maxInFlight.Load()
		if current <= observed || s.maxInFlight.CompareAndSwap(observed, current) {
			return
		}
	}
}

func (s *concurrencyRecordingStore) leave() { s.inFlight.Add(-1) }

func (s *concurrencyRecordingStore) projections() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.syncCalls
}

func newConcurrencyTestService(
	t *testing.T,
	store VectorStore,
	controlStore ControlStore,
) (*DocumentIndexService, *countingControlStore) {
	t.Helper()
	counting := &countingControlStore{inner: controlStore}
	core, err := NewServiceWithStore(context.Background(), store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })
	service, err := NewDocumentIndexServiceWithControlStore(context.Background(), core, counting)
	if err != nil {
		t.Fatal(err)
	}
	return service, counting
}

// TestSearchesProbeGenerationWithoutLoadingTheControlPlane is the executable
// form of the P3.2 read-path requirement: a search that finds the published
// snapshot current must not load the complete control plane.
func TestSearchesProbeGenerationWithoutLoadingTheControlPlane(t *testing.T) {
	store := &concurrencyRecordingStore{MemoryStore: NewMemoryStore()}
	service, counting := newConcurrencyTestService(t, store, NewMemoryControlStore())

	mustIndexContractVersion(t, service, "read-path", "v1", "owner", 1, "readpathneedle")
	mustActivateContractVersion(t, service, "read-path", "v1", 1, 1)

	loadsAfterWrites := counting.loads.Load()
	if loadsAfterWrites == 0 {
		t.Fatal("writes did not load the control plane, so the assertion below is vacuous")
	}

	const searches = 25
	for index := 0; index < searches; index++ {
		assertContractHitCount(t, service, SearchDocumentsRequest{
			Query: "readpathneedle", AllowedSpaceIDs: []string{"owner"}, TopK: 1,
		}, 1)
	}

	if got := counting.loads.Load(); got != loadsAfterWrites {
		t.Fatalf("searches loaded the control plane %d times, want 0", got-loadsAfterWrites)
	}
	if got := counting.generations.Load(); got < searches {
		t.Fatalf("generation probes = %d, want at least one per search (%d)", got, searches)
	}
}

// TestConcurrentSearchesRunInParallel records the behaviour the old global
// exclusive lock prevented: several searches inside the vector store at once.
func TestConcurrentSearchesRunInParallel(t *testing.T) {
	ctx := context.Background()
	store := &concurrencyRecordingStore{MemoryStore: NewMemoryStore(), denseDelay: 25 * time.Millisecond}
	service, counting := newConcurrencyTestService(t, store, NewMemoryControlStore())

	mustIndexContractVersion(t, service, "parallel", "v1", "owner", 1, "parallelneedle")
	mustActivateContractVersion(t, service, "parallel", "v1", 1, 1)
	loadsBeforeSearches := counting.loads.Load()

	const searches = 8
	var group sync.WaitGroup
	errs := make(chan error, searches)
	for index := 0; index < searches; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			result, err := service.SearchDocuments(ctx, SearchDocumentsRequest{
				Query: "parallelneedle", AllowedSpaceIDs: []string{"owner"}, TopK: 1,
			})
			if err != nil {
				errs <- err
				return
			}
			if len(result.Hits) != 1 {
				errs <- fmt.Errorf("concurrent search hits = %d, want 1", len(result.Hits))
			}
		}()
	}
	group.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent search: %v", err)
	}

	if observed := store.maxInFlight.Load(); observed < 2 {
		t.Fatalf("max concurrent searches inside the vector store = %d, want more than one", observed)
	}
	// Parallel reads must also stay free of control-plane reloads.
	if got := counting.loads.Load(); got != loadsBeforeSearches {
		t.Fatalf("concurrent searches loaded the control plane %d times, want 0", got-loadsBeforeSearches)
	}
}

// TestProjectionReconcilerConvergesWithoutARequest proves the projection is
// maintained in the background rather than by the search that follows a write.
func TestProjectionReconcilerConvergesWithoutARequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store := &recordingControlledStore{MemoryStore: NewMemoryStore()}
	core, err := NewServiceWithStore(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })
	service, err := NewDocumentIndexServiceWithControlStore(ctx, core, NewMemoryControlStore())
	if err != nil {
		t.Fatal(err)
	}
	service.StartProjectionReconciler(ctx)

	mustIndexContractVersion(t, service, "reconciled", "v1", "owner", 1, "reconciledneedle")
	mustActivateContractVersion(t, service, "reconciled", "v1", 1, 1)

	// Wait for the background reconciler to catch up with the published
	// generation, without issuing any request. Waiting for "a sync happened"
	// would be racy: an earlier write's sync must not satisfy the wait.
	deadline := time.Now().Add(3 * time.Second)
	for {
		snapshot := service.snapshot.Load()
		if snapshot.generation > 0 && service.projectionGeneration() >= snapshot.generation {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf(
				"projection was not converged in the background: synced=%d published=%d",
				service.projectionGeneration(), snapshot.generation,
			)
		}
		time.Sleep(5 * time.Millisecond)
	}

	// A search now finds the projection current and must not write it again.
	before := store.projectionCalls()
	assertContractHitCount(t, service, SearchDocumentsRequest{
		Query: "reconciledneedle", AllowedSpaceIDs: []string{"owner"}, TopK: 1,
	}, 1)
	if after := store.projectionCalls(); after != before {
		t.Fatalf("search wrote the projection %d times, want 0", after-before)
	}
}

// TestSearchStillFailsClosedWhenTheBackgroundProjectionIsBroken guards the
// safety property the read path keeps even though it no longer syncs eagerly.
func TestSearchStillFailsClosedWhenTheBackgroundProjectionIsBroken(t *testing.T) {
	ctx := context.Background()
	store := &projectionFailingStore{MemoryStore: NewMemoryStore()}
	service, _ := newConcurrencyTestService(t, store, NewMemoryControlStore())

	mustIndexContractVersion(t, service, "broken-projection", "v1", "owner", 1, "brokenneedle")
	mustActivateContractVersion(t, service, "broken-projection", "v1", 1, 1)

	if _, err := service.SearchDocuments(ctx, SearchDocumentsRequest{
		Query: "brokenneedle", AllowedSpaceIDs: []string{"owner"}, TopK: 1,
	}); err == nil {
		t.Fatal("search succeeded while the control projection could not be written")
	}
	if store.denseCalls.Load() != 0 || store.sparseCalls.Load() != 0 {
		t.Fatal("recall ran after the control projection failed")
	}
}

// blockingIngestStore holds one vector write open until the test releases it, so
// a writer can be kept in flight while other requests run.
type blockingIngestStore struct {
	*MemoryStore
	entered sync.Once
	started chan struct{}
	release chan struct{}
	block   atomic.Bool
}

func newBlockingIngestStore() *blockingIngestStore {
	return &blockingIngestStore{
		MemoryStore: NewMemoryStore(),
		started:     make(chan struct{}),
		release:     make(chan struct{}),
	}
}

func (s *blockingIngestStore) ReplaceDocument(ctx context.Context, documentID string, chunks []IndexedChunk) error {
	if s.block.Load() {
		s.entered.Do(func() { close(s.started) })
		select {
		case <-s.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return s.MemoryStore.ReplaceDocument(ctx, documentID, chunks)
}

// TestSearchesAreNotBlockedByAnIndexWriteInFlight pins the property the old
// global exclusive lock could not provide: while one request is inside a slow
// vector write, an unrelated search still completes.
func TestSearchesAreNotBlockedByAnIndexWriteInFlight(t *testing.T) {
	ctx := context.Background()
	store := newBlockingIngestStore()
	service, _ := newConcurrencyTestService(t, store, NewMemoryControlStore())

	// A searchable document exists before the slow write starts.
	mustIndexContractVersion(t, service, "steady", "v1", "owner", 1, "steadyneedle")
	mustActivateContractVersion(t, service, "steady", "v1", 1, 1)
	assertContractHitCount(t, service, SearchDocumentsRequest{
		Query: "steadyneedle", AllowedSpaceIDs: []string{"owner"}, TopK: 1,
	}, 1)

	store.block.Store(true)
	writeDone := make(chan error, 1)
	go func() {
		_, err := service.IndexDocumentVersion(ctx, IndexDocumentVersionRequest{
			OperationID: "slow-write", DocumentID: "slow", VersionID: "v1", OwnerSpaceID: "owner",
			Filename: "slow.md", Content: []byte("slowneedle"), LifecycleRevision: 1,
		})
		writeDone <- err
	}()

	select {
	case <-store.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the index write never reached the vector store")
	}

	// The writer is now inside its vector write. A search must not wait for it.
	searchDone := make(chan error, 1)
	go func() {
		result, err := service.SearchDocuments(ctx, SearchDocumentsRequest{
			Query: "steadyneedle", AllowedSpaceIDs: []string{"owner"}, TopK: 1,
		})
		if err == nil && len(result.Hits) != 1 {
			err = fmt.Errorf("hits = %d, want 1", len(result.Hits))
		}
		searchDone <- err
	}()

	select {
	case err := <-searchDone:
		if err != nil {
			t.Fatalf("search during an in-flight index write failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		close(store.release)
		t.Fatal("search was blocked by the in-flight index write")
	}

	close(store.release)
	if err := <-writeDone; err != nil {
		t.Fatalf("index write: %v", err)
	}
}

// TestLiveWriteIntentDoesNotForceAReaderReload guards the trigger itself: an
// unexpired write intent belongs to the writer that created it, so it must not
// be treated as maintenance a reader has to run.
func TestLiveWriteIntentDoesNotForceAReaderReload(t *testing.T) {
	snapshot := newControlSnapshot()
	snapshot.pendingVectorWrites["storage"] = ControlPendingVectorWrite{
		OperationID:             "operation",
		Fingerprint:             "fingerprint",
		LeaseExpiresAtUnixMilli: 2_000,
	}
	if snapshot.needsMaintenance(1_000) {
		t.Fatal("a live write intent was reported as pending maintenance")
	}
	if !snapshot.needsMaintenance(2_000) {
		t.Fatal("an expired write intent was not reported as pending maintenance")
	}

	snapshot.pendingVectorDeletes["storage"] = struct{}{}
	delete(snapshot.pendingVectorWrites, "storage")
	if !snapshot.needsMaintenance(1_000) {
		t.Fatal("a pending vector delete was not reported as pending maintenance")
	}
}

type projectionFailingStore struct {
	*MemoryStore
	denseCalls  atomic.Int64
	sparseCalls atomic.Int64
}

func (s *projectionFailingStore) SyncDocumentControls(context.Context, []VectorDocumentControl) error {
	return errProjectionUnavailable
}

func (s *projectionFailingStore) DenseSearchFiltered(
	ctx context.Context,
	query []float64,
	limit int,
	_ VectorSearchFilter,
) ([]ScoredChunk, error) {
	s.denseCalls.Add(1)
	return s.MemoryStore.DenseSearch(ctx, query, limit)
}

func (s *projectionFailingStore) SparseSearchFiltered(
	ctx context.Context,
	queryTokens []string,
	limit int,
	_ VectorSearchFilter,
) ([]ScoredChunk, error) {
	s.sparseCalls.Add(1)
	return s.MemoryStore.SparseSearch(ctx, queryTokens, limit)
}

var errProjectionUnavailable = &projectionUnavailableError{}

type projectionUnavailableError struct{}

func (*projectionUnavailableError) Error() string { return "control projection unavailable" }
