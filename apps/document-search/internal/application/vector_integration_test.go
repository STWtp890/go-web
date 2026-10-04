package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"document-search/internal/dbtest"
	"document-search/internal/infrastructure/qdrant"

	"packages/serviceauth"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The tests in this file prove the vector flow against the real Qdrant the
// development stack runs: an upsert writes points, a newer version replaces them
// instead of piling up, a delete removes them, a rebuild restores them from local
// data alone, the collection identity comes from the generation record, and the
// query arm recalls a document the keyword arm cannot match.
//
// Qdrant being unreachable fails every fixture (see openVectorIndex); nothing
// here is skipped.

// longContent builds content that produces several chunks under the development
// index profile (one chunk per ~800 characters).
func longContent(marker string, paragraphs int) string {
	pieces := make([]string, 0, paragraphs)
	for index := 0; index < paragraphs; index++ {
		pieces = append(pieces, fmt.Sprintf("paragraph %d of %s %s", index, marker, strings.Repeat("lorem ipsum dolor sit amet ", 40)))
	}
	return strings.Join(pieces, "\n\n")
}

// documentProfile reads the profile a document's vector was written under.
func (fixture *testFixture) documentProfile(documentID string) (string, int32) {
	fixture.t.Helper()
	var profile string
	var dimensions int32
	if err := fixture.pool.Pgx().QueryRow(fixture.ctx,
		`SELECT vector_profile, vector_dimensions FROM document_search.document_index WHERE document_id = $1::text::uuid`,
		documentID).Scan(&profile, &dimensions); err != nil {
		fixture.t.Fatalf("document-search integration: read the document's vector profile: %v", err)
	}
	return profile, dimensions
}

// pointCount reports how many vector points a document currently has.
func (fixture *testFixture) pointCount(documentID string) uint64 {
	fixture.t.Helper()
	count, err := fixture.vectors.PointCount(fixture.ctx, documentID)
	if err != nil {
		fixture.t.Fatalf("document-search integration: count the document's points: %v", err)
	}
	return count
}

// recalledVersion asks the vector collection for one space and returns the
// version of the recalled document.
func (fixture *testFixture) recalledVersion(t *testing.T, space, documentID, query string) string {
	t.Helper()
	candidates, err := fixture.vectors.Search(fixture.ctx, embedText32(query), 10, VectorFilter{SpaceIDs: []string{space}})
	if err != nil {
		t.Fatalf("document-search integration: recall from the collection: %v", err)
	}
	for _, candidate := range candidates {
		if candidate.DocumentID == documentID {
			return candidate.VersionID
		}
	}
	t.Fatalf("the collection did not recall document %s (candidates: %+v)", documentID, candidates)
	return ""
}

// ---------------------------------------------------------------------------
// 17. an upsert writes the document's points, a newer version replaces them
// ---------------------------------------------------------------------------

func TestIntegrationVectorUpsertWritesAndReplacesPoints(t *testing.T) {
	fixture := newTestFixture(t, "")
	space := fixture.newSpace()
	documentID := fixture.newDocument()
	token := "vectortoken" + dbtest.RunTag()

	first := fixture.upsertRequest(documentSpec{
		DocumentID:      documentID,
		OwnerSpaceID:    space,
		Title:           "Vector " + token,
		Content:         longContent(token, 2),
		AllowedSpaceIDs: []string{space},
	})
	response := fixture.apply(first)
	chunks := response.GetState().GetChunkCount()
	if chunks < 2 {
		t.Fatalf("the fixture content produced %d chunk(s); the replacement assertion needs at least two", chunks)
	}
	if points := fixture.pointCount(documentID); points != uint64(chunks) {
		t.Fatalf("the collection holds %d points for a document with chunk_count=%d", points, chunks)
	}
	if profile, dimensions := fixture.documentProfile(documentID); profile != VectorProfileLocalHashV1 || dimensions != int32(localHashDimensions) {
		t.Fatalf("the index row records profile %q/%d, want %q/%d",
			profile, dimensions, VectorProfileLocalHashV1, localHashDimensions)
	}
	if version := fixture.recalledVersion(t, space, documentID, token); version != first.GetVersionId() {
		t.Fatalf("the collection recalled version %q, want %q", version, first.GetVersionId())
	}

	// A shorter version must replace the points rather than leave the old chunks
	// behind: the second version has one chunk where the first had several.
	second := fixture.upsertRequest(documentSpec{
		DocumentID:         documentID,
		OwnerSpaceID:       space,
		Title:              "Vector " + token,
		Content:            "short " + token,
		AllowedSpaceIDs:    []string{space},
		AggregateRevision:  2,
		ActivationRevision: 2,
		AccessRevision:     2,
		LifecycleRevision:  2,
	})
	updated := fixture.apply(second)
	if updated.GetState().GetChunkCount() != 1 {
		t.Fatalf("the shorter version produced %d chunks, want 1", updated.GetState().GetChunkCount())
	}
	if points := fixture.pointCount(documentID); points != 1 {
		t.Fatalf("the collection holds %d points after the replacement, want exactly the new version's chunk", points)
	}
	if version := fixture.recalledVersion(t, space, documentID, token); version != second.GetVersionId() {
		t.Fatalf("the collection recalled version %q, want the new version %q", version, second.GetVersionId())
	}
}

// ---------------------------------------------------------------------------
// 18. a delete removes the points
// ---------------------------------------------------------------------------

func TestIntegrationVectorDeleteRemovesPoints(t *testing.T) {
	fixture := newTestFixture(t, "")
	space := fixture.newSpace()
	documentID := fixture.newDocument()
	token := "deletevector" + dbtest.RunTag()

	fixture.apply(fixture.upsertRequest(documentSpec{
		DocumentID:      documentID,
		OwnerSpaceID:    space,
		Title:           "Doomed vector " + token,
		Content:         longContent(token, 2),
		AllowedSpaceIDs: []string{space},
	}))
	if points := fixture.pointCount(documentID); points == 0 {
		t.Fatal("the collection holds no points for the indexed document")
	}

	deleted := fixture.apply(fixture.deleteRequest(documentID, 9, 9))
	if !deleted.GetApplied() {
		t.Fatalf("the delete was not applied: %q", deleted.GetReason())
	}
	if points := fixture.pointCount(documentID); points != 0 {
		t.Fatalf("the collection still holds %d points after the delete", points)
	}
	candidates, err := fixture.vectors.Search(fixture.ctx, embedText32(token), 10, VectorFilter{SpaceIDs: []string{space}})
	if err != nil {
		t.Fatalf("document-search integration: recall after the delete: %v", err)
	}
	for _, candidate := range candidates {
		if candidate.DocumentID == documentID {
			t.Fatalf("a deleted document is still recalled: %+v", candidate)
		}
	}

	// A fenced (stale) upsert must not write points either: the fence decides
	// before anything reaches the collection.
	stale := fixture.apply(fixture.upsertRequest(documentSpec{
		DocumentID:        documentID,
		OwnerSpaceID:      space,
		Title:             "Zombie vector " + token,
		Content:           longContent(token, 2),
		AllowedSpaceIDs:   []string{space},
		AggregateRevision: 8,
		AccessRevision:    8,
		LifecycleRevision: 8,
	}))
	if stale.GetApplied() || stale.GetReason() != ReasonStaleLifecycle {
		t.Fatalf("a stale upsert returned applied=%v reason=%q, want %q", stale.GetApplied(), stale.GetReason(), ReasonStaleLifecycle)
	}
	if points := fixture.pointCount(documentID); points != 0 {
		t.Fatalf("a fenced upsert wrote %d points; the revision fence must decide before the collection is touched", points)
	}
}

// ---------------------------------------------------------------------------
// 19. a rebuild restores the collection from local data alone
// ---------------------------------------------------------------------------

func TestIntegrationVectorRebuildRestoresPointsWithoutFactSource(t *testing.T) {
	fixture := newTestFixture(t, "")
	space := fixture.newSpace()
	documentID := fixture.newDocument()
	token := "rebuildvector" + dbtest.RunTag()

	response := fixture.apply(fixture.upsertRequest(documentSpec{
		DocumentID:      documentID,
		OwnerSpaceID:    space,
		Title:           "Rebuilt vector " + token,
		Content:         longContent(token, 2),
		AllowedSpaceIDs: []string{space},
	}))
	chunks := response.GetState().GetChunkCount()

	// Destroy the vector half only, exactly as a lost collection would look. The
	// applied event log is untouched, and the source endpoint of this fixture is a
	// closed port for the whole test.
	if err := fixture.vectors.DeleteDocuments(fixture.ctx, []string{documentID}); err != nil {
		t.Fatalf("document-search integration: clear the points: %v", err)
	}
	if points := fixture.pointCount(documentID); points != 0 {
		t.Fatalf("the fixture still holds %d points", points)
	}

	rebuilt, err := fixture.service.Rebuild(fixture.ctx, true)
	if err != nil {
		t.Fatalf("Rebuild with the source at %s: %v", closedSourceEndpoint, err)
	}
	if rebuilt.GetDocumentsFailed() != 0 {
		t.Fatalf("the rebuild reported %d failed events", rebuilt.GetDocumentsFailed())
	}
	if points := fixture.pointCount(documentID); points != uint64(chunks) {
		t.Fatalf("the rebuild restored %d points, want the document's %d chunks", points, chunks)
	}
	if version := fixture.recalledVersion(t, space, documentID, token); version != response.GetState().GetVersionId() {
		t.Fatalf("the rebuilt collection recalled version %q, want %q", version, response.GetState().GetVersionId())
	}

	// The rebuild also cleaned up superseded collections, and the live one is
	// still the collection the generation record names.
	store := fixture.vectorStore(t)
	physical, err := store.PhysicalCollection(fixture.ctx)
	if err != nil {
		t.Fatalf("resolve the alias after the rebuild: %v", err)
	}
	if want := qdrant.GenerationCollection(store.Alias(), store.Generation()); physical != want {
		t.Fatalf("the alias resolves to %q after the rebuild, want %q", physical, want)
	}
}

// ---------------------------------------------------------------------------
// 20. hybrid search recalls what the keyword arm cannot match
// ---------------------------------------------------------------------------

func TestIntegrationHybridSearchRecallsVectorOnlyDocument(t *testing.T) {
	fixture := newTestFixture(t, "")
	space := fixture.newSpace()
	otherSpace := fixture.newSpace()
	keywordDocument := fixture.newDocument()
	vectorOnlyDocument := fixture.newDocument()
	outOfScopeDocument := fixture.newDocument()
	query := "goroutine channel"

	fixture.apply(fixture.upsertRequest(documentSpec{
		DocumentID:      keywordDocument,
		OwnerSpaceID:    space,
		Title:           "Concurrency notes",
		Content:         "The goroutine channel pattern in Go.",
		AllowedSpaceIDs: []string{space},
	}))
	// This document shares one term with the query, so it is not a keyword match
	// (the tsquery requires both terms) while the vector arm still recalls it.
	fixture.apply(fixture.upsertRequest(documentSpec{
		DocumentID:      vectorOnlyDocument,
		OwnerSpaceID:    space,
		Title:           "Scheduling notes",
		Content:         "A goroutine is scheduled by the runtime.",
		AllowedSpaceIDs: []string{space},
	}))
	// Same content as the keyword document, in a space the capability does not
	// grant: it must never appear.
	fixture.apply(fixture.upsertRequest(documentSpec{
		DocumentID:      outOfScopeDocument,
		OwnerSpaceID:    otherSpace,
		Title:           "Concurrency notes elsewhere",
		Content:         "The goroutine channel pattern in Go.",
		AllowedSpaceIDs: []string{otherSpace},
	}))

	capability := subjectCapability("web:user:hybrid", false, []string{space}, nil)
	request := searchPage(query, []string{space}, nil, 1, 10, false)

	// The keyword-only configuration of the same service is the control: without
	// the vector arm the second document is not in the answer at all.
	keywordOnly := fixture.keywordOnlyService(t)
	baseline, err := keywordOnly.Search(fixture.ctx, nil, capability, request)
	if err != nil {
		t.Fatalf("keyword-only search: %v", err)
	}
	if len(baseline.GetHits()) != 1 || !hasHit(baseline, keywordDocument) {
		t.Fatalf("the keyword-only answer is %v, want only the document that contains both terms", hitDocuments(baseline))
	}

	hybrid := fixture.search(capability, request)
	if hybrid.GetTotal() != 2 {
		t.Fatalf("hybrid total = %d (%v), want the keyword match plus the vector-only document",
			hybrid.GetTotal(), hitDocuments(hybrid))
	}
	if !hasHit(hybrid, keywordDocument) || !hasHit(hybrid, vectorOnlyDocument) {
		t.Fatalf("the hybrid answer is %v, want both the keyword and the vector-only document", hitDocuments(hybrid))
	}
	if hasHit(hybrid, outOfScopeDocument) {
		t.Fatal("the vector arm recalled a document outside the granted scope")
	}
	// The document both arms found outranks the one only the vector arm recalled.
	if hybrid.GetHits()[0].GetDocumentId() != keywordDocument {
		t.Fatalf("the fused order starts with %s, want the keyword document %s",
			hybrid.GetHits()[0].GetDocumentId(), keywordDocument)
	}
	if hybrid.GetTruncated() {
		t.Fatal("a two-document answer reported truncated")
	}
	// The vector-only hit is presented as a real document, not as a bare id.
	vectorOnly := findHit(t, hybrid, vectorOnlyDocument)
	if vectorOnly.GetTitle() == "" || vectorOnly.GetSnippet() == "" {
		t.Fatalf("the vector-only hit carries no display fields: %+v", vectorOnly)
	}

	// Paging over the fused order stays disjoint: page one holds one document,
	// page two the other, and both pages report the same real total.
	firstPage := fixture.search(capability, searchPage(query, []string{space}, nil, 1, 1, false))
	secondPage := fixture.search(capability, searchPage(query, []string{space}, nil, 2, 1, false))
	if len(firstPage.GetHits()) != 1 || len(secondPage.GetHits()) != 1 {
		t.Fatalf("paging the fused order returned %d and %d hits, want one each",
			len(firstPage.GetHits()), len(secondPage.GetHits()))
	}
	if firstPage.GetHits()[0].GetDocumentId() == secondPage.GetHits()[0].GetDocumentId() {
		t.Fatal("two pages of the fused order returned the same document")
	}
	if firstPage.GetTotal() != 2 || secondPage.GetTotal() != 2 {
		t.Fatalf("paged totals = %d and %d, want the real total 2", firstPage.GetTotal(), secondPage.GetTotal())
	}
	if !firstPage.GetTruncated() || secondPage.GetTruncated() {
		t.Fatalf("truncated flags = %v and %v, want true then false", firstPage.GetTruncated(), secondPage.GetTruncated())
	}

	// The owner filter applies to the recall as well: both documents belong to
	// another subject, so a personal search must not return them.
	ownedCapability := subjectCapability("web:user:someone-else", false, []string{space}, nil)
	personal := fixture.search(ownedCapability, searchPage(query, []string{space}, nil, 1, 10, true))
	if hasHit(personal, vectorOnlyDocument) || hasHit(personal, keywordDocument) {
		t.Fatalf("a personal search returned another subject's documents: %v", hitDocuments(personal))
	}
}

// keywordOnlyService builds the same service with the vector flow switched off,
// which is the control the hybrid answer is compared against.
func (fixture *testFixture) keywordOnlyService(t *testing.T) *Service {
	t.Helper()
	cfg := fixture.cfg
	cfg.Vector.Enabled = false
	codec, err := serviceauth.NewCodec(testServiceKey)
	if err != nil {
		t.Fatalf("document-search integration: boundary codec: %v", err)
	}
	service, err := New(Dependencies{Config: cfg, Pool: fixture.pool, Codec: codec})
	if err != nil {
		t.Fatalf("document-search integration: keyword-only service: %v", err)
	}
	return service
}

// ---------------------------------------------------------------------------
// 21. the collection identity comes from the generation record
// ---------------------------------------------------------------------------

func TestIntegrationVectorNamespaceMatchesTheActiveGeneration(t *testing.T) {
	fixture := newTestFixture(t, "")
	store := fixture.vectorStore(t)

	var generation, alias, domain, profile string
	var dimensions int32
	if err := fixture.pool.Pgx().QueryRow(fixture.ctx,
		`SELECT generation, collection_alias, storage_domain, vector_profile, vector_dimensions
		 FROM document_search.index_generations WHERE active`).Scan(&generation, &alias, &domain, &profile, &dimensions); err != nil {
		t.Fatalf("read the active generation: %v", err)
	}
	if store.Alias() != alias || store.StorageDomain() != domain || store.Generation() != generation {
		t.Fatalf("the opened collection is %s/%s/%s, want the recorded %s/%s/%s",
			store.Generation(), store.Alias(), store.StorageDomain(), generation, alias, domain)
	}
	if store.Profile() != profile || store.Dimensions() != int(dimensions) {
		t.Fatalf("the opened collection uses profile %q/%d, want the recorded %q/%d",
			store.Profile(), store.Dimensions(), profile, dimensions)
	}
	physical, err := store.PhysicalCollection(fixture.ctx)
	if err != nil {
		t.Fatalf("resolve the alias: %v", err)
	}
	if want := qdrant.GenerationCollection(alias, generation); physical != want {
		t.Fatalf("the alias resolves to %q, want %q", physical, want)
	}
	if !strings.HasPrefix(physical, alias+"_") {
		t.Fatalf("the physical collection %q is outside the alias namespace %q", physical, alias)
	}
	// The status RPC reports the same identity, so an operator reads the record
	// rather than a second copy of it.
	status, err := fixture.service.Status(fixture.ctx)
	if err != nil {
		t.Fatalf("GetIndexStatus: %v", err)
	}
	if status.GetCollectionAlias() != alias || status.GetGeneration() != generation {
		t.Fatalf("status reports %s/%s, want the recorded %s/%s",
			status.GetGeneration(), status.GetCollectionAlias(), generation, alias)
	}
}

// ---------------------------------------------------------------------------
// 22. generations can be prepared, switched, pruned and dropped
// ---------------------------------------------------------------------------

func TestIntegrationVectorGenerationLifecycleOnAScratchAlias(t *testing.T) {
	fixture := newTestFixture(t, "")
	scoped := fixture.scratchVectorStore(t, "lifecycle", "g1")
	if err := scoped.EnsureGeneration(fixture.ctx); err != nil {
		t.Fatalf("ensure the scratch generation: %v", err)
	}
	// The scratch alias is fully removed when the test ends, whatever it did: the
	// alias first, then every collection under it.
	t.Cleanup(func() {
		ctx := context.Background()
		if err := scoped.DropAlias(ctx); err != nil {
			t.Errorf("drop the scratch alias: %v", err)
		}
		if _, err := scoped.PruneGenerations(ctx, nil); err != nil {
			t.Errorf("prune the scratch collections: %v", err)
		}
		if err := scoped.Close(); err != nil {
			t.Errorf("close the scratch store: %v", err)
		}
	})

	physicalA, err := scoped.PhysicalCollection(fixture.ctx)
	if err != nil {
		t.Fatalf("resolve the scratch alias: %v", err)
	}
	if want := qdrant.GenerationCollection(scoped.Alias(), "g1"); physicalA != want {
		t.Fatalf("the scratch alias resolves to %q, want %q", physicalA, want)
	}

	// A second generation can be filled before it is served.
	physicalB, err := scoped.PrepareGeneration(fixture.ctx, "g2")
	if err != nil {
		t.Fatalf("prepare generation g2: %v", err)
	}
	if physicalB == physicalA {
		t.Fatal("generation g2 resolved to generation g1's collection")
	}
	document := VectorDocument{
		DocumentID:    dbtest.Identifier(t),
		VersionID:     dbtest.Identifier(t),
		OwnerSpaceID:  dbtest.Identifier(t),
		VectorProfile: VectorProfileLocalHashV1,
		Chunks:        []VectorChunk{{Index: 0, Vector: embedText32("scratch document")}},
	}
	if err := scoped.ReplaceDocuments(fixture.ctx, []VectorDocument{document}); err != nil {
		t.Fatalf("write to the scratch generation: %v", err)
	}
	if count, err := scoped.PointCount(fixture.ctx, document.DocumentID); err != nil || count != 1 {
		t.Fatalf("the scratch generation holds %d points (err %v), want 1", count, err)
	}

	// Switching moves the alias; the new generation starts empty and is filled by
	// whatever writes happen next.
	if err := scoped.SwitchAlias(fixture.ctx, physicalB); err != nil {
		t.Fatalf("switch the scratch alias to g2: %v", err)
	}
	current, err := scoped.PhysicalCollection(fixture.ctx)
	if err != nil {
		t.Fatalf("resolve the scratch alias after the switch: %v", err)
	}
	if current != physicalB {
		t.Fatalf("the scratch alias resolves to %q after the switch, want %q", current, physicalB)
	}
	if count, err := scoped.PointCount(fixture.ctx, document.DocumentID); err != nil || count != 0 {
		t.Fatalf("the empty generation reports %d points (err %v), want 0", count, err)
	}
	// A target outside this corpus is refused, so a typo cannot take the alias
	// somewhere else.
	if err := scoped.SwitchAlias(fixture.ctx, "go_web_document_v1_g1"); err == nil {
		t.Fatal("the alias switched to another corpus's collection")
	}
	// The live collection cannot be dropped out from under the alias.
	if err := scoped.DropCollection(fixture.ctx, physicalB); err == nil {
		t.Fatal("the collection the alias serves was dropped")
	}

	// Pruning removes the generations no record references: g1 is a leftover now
	// and g2 is live, so with an empty keep-set only g1 goes.
	dropped, err := scoped.PruneGenerations(fixture.ctx, nil)
	if err != nil {
		t.Fatalf("prune the scratch generations: %v", err)
	}
	if len(dropped) != 1 || dropped[0] != physicalA {
		t.Fatalf("pruning dropped %v, want exactly the superseded %q", dropped, physicalA)
	}
	if _, err := scoped.PhysicalCollection(fixture.ctx); err != nil {
		t.Fatalf("the alias did not survive the prune: %v", err)
	}

	// A recorded generation is kept even when it is not the live one: the record
	// is what decides.
	physicalC, err := scoped.PrepareGeneration(fixture.ctx, "g3")
	if err != nil {
		t.Fatalf("prepare generation g3: %v", err)
	}
	kept, err := scoped.PruneGenerations(fixture.ctx, []string{physicalC})
	if err != nil {
		t.Fatalf("prune with a keep-set: %v", err)
	}
	if len(kept) != 0 {
		t.Fatalf("pruning dropped %v although the record references them", kept)
	}
	if err := scoped.DropCollection(fixture.ctx, physicalC); err != nil {
		t.Fatalf("drop the recorded generation explicitly: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 23. a vector failure rolls the SQL side back
// ---------------------------------------------------------------------------

func TestIntegrationVectorFailureRollsBackTheIndexTransaction(t *testing.T) {
	fixture := newTestFixture(t, "")
	space := fixture.newSpace()
	documentID := fixture.newDocument()
	token := "vectorfailure" + dbtest.RunTag()

	failing := &failingVectorIndex{VectorIndex: fixture.vectors, err: errors.New("the vector backend is down")}
	cfg := fixture.cfg
	codec, err := serviceauth.NewCodec(testServiceKey)
	if err != nil {
		t.Fatalf("document-search integration: boundary codec: %v", err)
	}
	service, err := New(Dependencies{Config: cfg, Pool: fixture.pool, Codec: codec, VectorIndex: failing})
	if err != nil {
		t.Fatalf("document-search integration: service with a failing vector index: %v", err)
	}

	request := fixture.upsertRequest(documentSpec{
		DocumentID:      documentID,
		OwnerSpaceID:    space,
		Title:           "Failing vector " + token,
		Content:         "body " + token,
		AllowedSpaceIDs: []string{space},
	})
	if response, err := service.ApplyEvent(fixture.ctx, request); err == nil {
		t.Fatalf("an apply with a failing vector index returned %+v", response)
	} else if code := status.Code(err); code != codes.Unavailable {
		t.Fatalf("the failure surfaced as %s (%v), want UNAVAILABLE", code, err)
	}
	if failing.replaced == 0 {
		t.Fatal("the vector write was never attempted")
	}

	// The event is still pending: no applied event row, no projection, and the
	// redelivery can apply both halves again.
	if rows := fixture.countRows(`SELECT count(*) FROM document_search.document_index WHERE document_id = $1::text::uuid`, documentID); rows != 0 {
		t.Fatalf("index rows = %d after the failed apply, want 0", rows)
	}
	if rows := fixture.countRows(`SELECT count(*) FROM document_search.document_index_events WHERE event_id = $1::text::uuid`, request.GetEventId()); rows != 0 {
		t.Fatalf("applied event rows = %d after the failed apply, want 0", rows)
	}

	// With the vector index healthy again, the same event applies.
	healthy, err := New(Dependencies{Config: cfg, Pool: fixture.pool, Codec: codec, VectorIndex: fixture.vectors})
	if err != nil {
		t.Fatalf("document-search integration: service with the healthy vector index: %v", err)
	}
	if _, err := healthy.ApplyEvent(fixture.ctx, request); err != nil {
		t.Fatalf("the redelivered event did not apply: %v", err)
	}
	if rows := fixture.countRows(`SELECT count(*) FROM document_search.document_index WHERE document_id = $1::text::uuid`, documentID); rows != 1 {
		t.Fatalf("index rows = %d after the redelivery, want 1", rows)
	}
	if points := fixture.pointCount(documentID); points == 0 {
		t.Fatal("the redelivered event wrote no points")
	}
}

// failingVectorIndex is the real collection with a failing write. Embedding the
// interface keeps every other method honest.
type failingVectorIndex struct {
	VectorIndex
	err      error
	replaced int
}

func (index *failingVectorIndex) ReplaceDocuments(context.Context, []VectorDocument) error {
	index.replaced++
	return index.err
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// vectorStore exposes the concrete store so a test can inspect the alias
// mapping, which is deliberately not part of the port the application uses.
func (fixture *testFixture) vectorStore(t *testing.T) *qdrant.Store {
	t.Helper()
	store, ok := fixture.vectors.(*qdrant.Store)
	if !ok {
		t.Fatalf("the fixture's vector index is %T, want the Qdrant store", fixture.vectors)
	}
	return store
}

// scratchVectorStore opens a second, isolated alias so the generation lifecycle
// can be exercised without disturbing the collection every other test uses.
func (fixture *testFixture) scratchVectorStore(t *testing.T, name, generation string) *qdrant.Store {
	t.Helper()
	settings, err := fixture.cfg.VectorConfig()
	if err != nil {
		t.Fatalf("document-search integration: vector settings: %v", err)
	}
	store, err := qdrant.New(qdrant.Config{
		Host:          settings.Host,
		Port:          settings.Port,
		APIKey:        settings.APIKey,
		UseTLS:        settings.UseTLS,
		Alias:         "go_web_document_scratch_" + name + "_" + dbtest.RunTag(),
		Generation:    generation,
		StorageDomain: "document-search:scratch:v1",
		VectorProfile: settings.Profile,
		Dimensions:    uint64(settings.Dimensions),
		Timeout:       settings.Timeout,
	})
	if err != nil {
		t.Fatalf("document-search integration: open the scratch collection: %v", err)
	}
	return store
}
