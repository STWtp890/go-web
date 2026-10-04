package application

import (
	"fmt"
	"testing"
	"time"

	"document-search/internal/dbtest"

	documentsearchv1 "packages/gen/documentsearch/v1"
	"packages/serviceauth"
)

// The tests in this file prove the query semantics against the real PostgreSQL:
// the request is the actual filter (not just a validated subset), a personal
// search never returns another subject's documents, pages are disjoint and the
// total is real, and every hit carries the fields a Web surface renders.

// subjectCapability mints a capability the way document-service would after
// resolving one subject's range.
func subjectCapability(subjectKey string, public bool, spaces, documents []string) *serviceauth.CapabilityClaims {
	return &serviceauth.CapabilityClaims{
		Version:             1,
		Issuer:              serviceauth.CallerDocumentService,
		Audience:            serviceauth.AudienceDocumentSearch,
		Scopes:              []string{string(serviceauth.ScopeDocumentSearcher)},
		SubjectKey:          subjectKey,
		AllowedSpaceIDs:     spaces,
		AllowedDocumentIDs:  documents,
		AuthenticatedPublic: public,
	}
}

// searchPage builds a query with the paging and ownership switches set.
func searchPage(query string, spaces, documents []string, page, pageSize int32, ownedBySubjectOnly bool) *documentsearchv1.SearchDocumentsRequest {
	return &documentsearchv1.SearchDocumentsRequest{
		Query:              query,
		AllowedSpaceIds:    spaces,
		AllowedDocumentIds: documents,
		Page:               page,
		PageSize:           pageSize,
		OwnedBySubjectOnly: ownedBySubjectOnly,
	}
}

// findHit returns the hit for a document, or fails the test.
func findHit(t *testing.T, response *documentsearchv1.SearchDocumentsResponse, documentID string) *documentsearchv1.SearchHit {
	t.Helper()
	for _, hit := range response.GetHits() {
		if hit.GetDocumentId() == documentID {
			return hit
		}
	}
	t.Fatalf("document %s is not in the response %v", documentID, hitDocuments(response))
	return nil
}

// newSearchFixtureWithMaxPageSize builds the same fixture with a lowered page
// size ceiling, so "above the configured maximum" is testable without changing
// the shared development defaults.
func newSearchFixtureWithMaxPageSize(t *testing.T, maxPageSize int) *testFixture {
	t.Helper()
	fixture := newTestFixture(t, "")
	fixture.cfg.Index.MaxTopK = maxPageSize
	codec, err := serviceauth.NewCodec(testServiceKey)
	if err != nil {
		t.Fatalf("document-search integration: boundary codec: %v", err)
	}
	service, err := New(Dependencies{Config: fixture.cfg, Pool: fixture.pool, Codec: codec, VectorIndex: fixture.vectors})
	if err != nil {
		t.Fatalf("document-search integration: application service: %v", err)
	}
	fixture.service = service
	return fixture
}

// ---------------------------------------------------------------------------
// 12. a named range is the actual filter, not just a validated subset
// ---------------------------------------------------------------------------

func TestIntegrationSearchNarrowingExcludesOtherSpaces(t *testing.T) {
	fixture := newTestFixture(t, "")
	spaceA := fixture.newSpace()
	spaceB := fixture.newSpace()
	documentInA := fixture.newDocument()
	documentInB := fixture.newDocument()
	publicInB := fixture.newDocument()
	token := "narrowtoken" + dbtest.RunTag()

	fixture.apply(fixture.upsertRequest(documentSpec{
		DocumentID:      documentInA,
		OwnerSpaceID:    spaceA,
		Title:           "Only A " + token,
		Content:         "body " + token,
		AllowedSpaceIDs: []string{spaceA},
	}))
	fixture.apply(fixture.upsertRequest(documentSpec{
		DocumentID:      documentInB,
		OwnerSpaceID:    spaceB,
		Title:           "Private B " + token,
		Content:         "body " + token,
		AllowedSpaceIDs: []string{spaceB},
	}))
	fixture.apply(fixture.upsertRequest(documentSpec{
		DocumentID:          publicInB,
		OwnerSpaceID:        spaceB,
		Title:               "Public B " + token,
		Content:             "body " + token,
		AllowedSpaceIDs:     []string{spaceB},
		AuthenticatedPublic: true,
	}))

	granted := subjectCapability("web:user:narrowing", false, []string{spaceA, spaceB}, nil)

	// Nothing named: the whole granted envelope is searched, so this fixture is
	// a real test of the narrowing below.
	everything := fixture.search(granted, searchPage(token, nil, nil, 1, 10, false))
	if everything.GetTotal() != 3 {
		t.Fatalf("total without narrowing = %d (%v), want all three granted documents",
			everything.GetTotal(), hitDocuments(everything))
	}

	// "Only space A" returns space A alone. Space B's private document and - the
	// defect this test exists for - space B's authenticated-public document must
	// both stay out.
	onlyA := fixture.search(granted, searchPage(token, []string{spaceA}, nil, 1, 10, false))
	if onlyA.GetTotal() != 1 || !hasHit(onlyA, documentInA) {
		t.Fatalf("naming space A returned %v (total %d), want only the document in A",
			hitDocuments(onlyA), onlyA.GetTotal())
	}
	if hasHit(onlyA, documentInB) {
		t.Fatal("naming space A returned a private document from space B")
	}
	if hasHit(onlyA, publicInB) {
		t.Fatal("naming space A returned an authenticated-public document from space B")
	}

	// The same request under a capability that carries the public floor: naming a
	// range must not re-enable that floor.
	publicGrant := subjectCapability("web:user:narrowing", true, []string{spaceA, spaceB}, nil)
	if all := fixture.search(publicGrant, searchPage(token, nil, nil, 1, 10, false)); all.GetTotal() != 3 {
		t.Fatalf("total with the public floor = %d, want all three", all.GetTotal())
	}
	narrowedPublic := fixture.search(publicGrant, searchPage(token, []string{spaceA}, nil, 1, 10, false))
	if narrowedPublic.GetTotal() != 1 || !hasHit(narrowedPublic, documentInA) {
		t.Fatalf("naming space A with the public floor returned %v, want only the document in A",
			hitDocuments(narrowedPublic))
	}

	// A capability that grants both spaces and a document-level range: the two
	// families are narrowed independently.
	withDocumentGrant := subjectCapability("web:user:narrowing", false, []string{spaceA, spaceB}, []string{publicInB})

	// Naming a document is the same rule in the other family: the granted spaces
	// are no longer an answer, only the named document is.
	onlyDocument := fixture.search(withDocumentGrant, searchPage(token, nil, []string{publicInB}, 1, 10, false))
	if onlyDocument.GetTotal() != 1 || !hasHit(onlyDocument, publicInB) {
		t.Fatalf("naming one document returned %v, want exactly that document", hitDocuments(onlyDocument))
	}
	if hasHit(onlyDocument, documentInA) {
		t.Fatal("naming a document still returned the other granted spaces")
	}

	// A document-level grant outside the named space is not part of "only space
	// A": the unnamed family contributes nothing once a family is named.
	spaceOnly := fixture.search(withDocumentGrant, searchPage(token, []string{spaceA}, nil, 1, 10, false))
	if spaceOnly.GetTotal() != 1 || !hasHit(spaceOnly, documentInA) {
		t.Fatalf("naming space A with a document-level grant elsewhere returned %v, want only the document in A",
			hitDocuments(spaceOnly))
	}

	// Both families named is the union of exactly what was named.
	union := fixture.search(withDocumentGrant, searchPage(token, []string{spaceA}, []string{publicInB}, 1, 10, false))
	if union.GetTotal() != 2 || !hasHit(union, documentInA) || !hasHit(union, publicInB) {
		t.Fatalf("naming a space and a document returned %v, want both named documents", hitDocuments(union))
	}

	// An identifier outside the grant rejects the whole request, even beside an
	// in-grant one and even when the query would have matched nothing.
	outside := fixture.newSpace()
	fixture.requireDenied(granted, searchPage(token, []string{spaceA, outside}, nil, 1, 10, false))
	fixture.requireDenied(granted, searchPage(token, nil, []string{documentInA}, 1, 10, false))
	fixture.requireDenied(granted, searchPage(token, []string{outside}, []string{documentInA}, 1, 10, false))
}

// ---------------------------------------------------------------------------
// 13. a personal search never returns another subject's documents
// ---------------------------------------------------------------------------

func TestIntegrationSearchOwnerSubjectsAreIsolated(t *testing.T) {
	fixture := newTestFixture(t, "")
	spaceA := fixture.newSpace()
	spaceB := fixture.newSpace()
	subject := "web:user:owner-" + dbtest.RunTag()
	other := "web:user:other-" + dbtest.RunTag()
	token := "ownertoken" + dbtest.RunTag()

	ownPrivate := fixture.newDocument()
	ownPublic := fixture.newDocument()
	otherPrivate := fixture.newDocument()
	otherPublic := fixture.newDocument()

	fixture.apply(fixture.upsertRequest(documentSpec{
		DocumentID:      ownPrivate,
		OwnerSpaceID:    spaceA,
		OwnerSubjectKey: subject,
		Title:           "Mine " + token,
		Content:         "body " + token,
		AllowedSpaceIDs: []string{spaceA},
	}))
	fixture.apply(fixture.upsertRequest(documentSpec{
		DocumentID:          ownPublic,
		OwnerSpaceID:        spaceA,
		OwnerSubjectKey:     subject,
		Title:               "Mine public " + token,
		Content:             "body " + token,
		AllowedSpaceIDs:     []string{spaceA},
		AuthenticatedPublic: true,
	}))
	fixture.apply(fixture.upsertRequest(documentSpec{
		DocumentID:      otherPrivate,
		OwnerSpaceID:    spaceB,
		OwnerSubjectKey: other,
		Title:           "Theirs " + token,
		Content:         "body " + token,
		AllowedSpaceIDs: []string{spaceB},
	}))
	fixture.apply(fixture.upsertRequest(documentSpec{
		DocumentID:          otherPublic,
		OwnerSpaceID:        spaceB,
		OwnerSubjectKey:     other,
		Title:               "Theirs public " + token,
		Content:             "body " + token,
		AllowedSpaceIDs:     []string{spaceB},
		AuthenticatedPublic: true,
	}))

	granted := subjectCapability(subject, true, []string{spaceA, spaceB}, nil)

	// Without the owner switch the granted range does reach every one of them,
	// so the exclusions below are the owner filter and not an empty index.
	everything := fixture.search(granted, searchPage(token, nil, nil, 1, 10, false))
	if everything.GetTotal() != 4 {
		t.Fatalf("total without the owner filter = %d (%v), want all four",
			everything.GetTotal(), hitDocuments(everything))
	}

	mine := fixture.search(granted, searchPage(token, nil, nil, 1, 10, true))
	if mine.GetTotal() != 2 {
		t.Fatalf("personal search total = %d (%v), want only the subject's two documents",
			mine.GetTotal(), hitDocuments(mine))
	}
	if !hasHit(mine, ownPrivate) || !hasHit(mine, ownPublic) {
		t.Fatalf("personal search lost the subject's own documents: %v", hitDocuments(mine))
	}
	if hasHit(mine, otherPrivate) {
		t.Fatal("a personal search returned another subject's private document")
	}
	if hasHit(mine, otherPublic) {
		t.Fatal("a personal search returned another subject's public document")
	}

	// Narrowing on top of the owner filter still works.
	narrowed := fixture.search(granted, searchPage(token, []string{spaceA}, nil, 1, 10, true))
	if narrowed.GetTotal() != 2 || hasHit(narrowed, otherPublic) {
		t.Fatalf("personal search inside space A returned %v, want the subject's own documents",
			hitDocuments(narrowed))
	}

	// A capability that names no subject cannot answer a personal search: the
	// fail-closed answer is no documents, not an unfiltered page.
	subjectless := subjectCapability("   ", true, []string{spaceA, spaceB}, nil)
	empty := fixture.search(subjectless, searchPage(token, nil, nil, 1, 10, true))
	if empty.GetTotal() != 0 || len(empty.GetHits()) != 0 {
		t.Fatalf("a subjectless personal search returned %v (total %d), want nothing",
			hitDocuments(empty), empty.GetTotal())
	}
}

// ---------------------------------------------------------------------------
// 14. paging is disjoint, complete and honest about the total
// ---------------------------------------------------------------------------

func TestIntegrationSearchPagesAreDisjointAndComplete(t *testing.T) {
	fixture := newTestFixture(t, "")
	space := fixture.newSpace()
	token := "pagetoken" + dbtest.RunTag()
	documentIDs := make([]string, 0, 5)
	for index := 0; index < 5; index++ {
		documentID := fixture.newDocument()
		documentIDs = append(documentIDs, documentID)
		fixture.apply(fixture.upsertRequest(documentSpec{
			DocumentID:      documentID,
			OwnerSpaceID:    space,
			Title:           fmt.Sprintf("Page %d %s", index, token),
			Content:         "body " + token,
			AllowedSpaceIDs: []string{space},
		}))
	}
	granted := subjectCapability("web:user:paging", false, []string{space}, nil)

	seen := make(map[string]int, len(documentIDs))
	for page := int32(1); page <= 3; page++ {
		response := fixture.search(granted, searchPage(token, []string{space}, nil, page, 2, false))
		if response.GetTotal() != 5 {
			t.Fatalf("page %d total = %d, want the real match count 5", page, response.GetTotal())
		}
		wantHits := 2
		if page == 3 {
			wantHits = 1
		}
		if len(response.GetHits()) != wantHits {
			t.Fatalf("page %d returned %d hits, want %d", page, len(response.GetHits()), wantHits)
		}
		if page < 3 && !response.GetTruncated() {
			t.Fatalf("page %d reported truncated=false while more matches exist", page)
		}
		if page == 3 && response.GetTruncated() {
			t.Fatal("the last page reported truncated=true")
		}
		for _, hit := range response.GetHits() {
			seen[hit.GetDocumentId()]++
		}
	}
	if len(seen) != len(documentIDs) {
		t.Fatalf("the three pages covered %d documents, want %d", len(seen), len(documentIDs))
	}
	for documentID, count := range seen {
		if count != 1 {
			t.Fatalf("document %s appeared %d times across the pages, want exactly once", documentID, count)
		}
	}

	// A page past the end is empty, and the total still describes the match set.
	beyond := fixture.search(granted, searchPage(token, []string{space}, nil, 9, 2, false))
	if len(beyond.GetHits()) != 0 || beyond.GetTotal() != 5 {
		t.Fatalf("a page past the end returned %d hits (total %d), want none and total 5",
			len(beyond.GetHits()), beyond.GetTotal())
	}

	// page_size 0 selects the configured default, which fits this fixture.
	defaulted := fixture.search(granted, searchPage(token, []string{space}, nil, 1, 0, false))
	if len(defaulted.GetHits()) != 5 || defaulted.GetTruncated() {
		t.Fatalf("the default page size returned %d hits (truncated %v), want all five",
			len(defaulted.GetHits()), defaulted.GetTruncated())
	}
	if pageOne := fixture.search(granted, searchPage(token, []string{space}, nil, 0, 0, false)); len(pageOne.GetHits()) != 5 {
		t.Fatalf("page 0 returned %d hits, want it treated as the first page", len(pageOne.GetHits()))
	}
}

func TestIntegrationSearchPageSizeAboveMaximumIsLoweredAndReported(t *testing.T) {
	fixture := newSearchFixtureWithMaxPageSize(t, 3)
	space := fixture.newSpace()
	token := "ceilingtoken" + dbtest.RunTag()
	for index := 0; index < 5; index++ {
		fixture.apply(fixture.upsertRequest(documentSpec{
			DocumentID:      fixture.newDocument(),
			OwnerSpaceID:    space,
			Title:           fmt.Sprintf("Ceiling %d %s", index, token),
			Content:         "body " + token,
			AllowedSpaceIDs: []string{space},
		}))
	}
	granted := subjectCapability("web:user:ceiling", false, []string{space}, nil)

	response := fixture.search(granted, searchPage(token, []string{space}, nil, 1, 50, false))
	if len(response.GetHits()) != 3 {
		t.Fatalf("a page size above the maximum returned %d hits, want it lowered to 3", len(response.GetHits()))
	}
	if !response.GetTruncated() {
		t.Fatal("a lowered page size was not reported as truncated")
	}
	if response.GetTotal() != 5 {
		t.Fatalf("total = %d, want the real match count 5", response.GetTotal())
	}
}

// ---------------------------------------------------------------------------
// 15. lifecycle, publication and delete keep rows out of the answer
// ---------------------------------------------------------------------------

func TestIntegrationSearchExcludesInactiveAndNonPublishedRows(t *testing.T) {
	fixture := newTestFixture(t, "")
	space := fixture.newSpace()
	token := "scopetoken" + dbtest.RunTag()
	live := fixture.newDocument()

	specs := []struct {
		name              string
		lifecycleStatus   string
		publicationStatus string
		documentID        string
	}{
		{"a draft version", "active", "draft", fixture.newDocument()},
		{"a withdrawn version", "active", "withdrawn", fixture.newDocument()},
		{"a superseded version", "active", "superseded", fixture.newDocument()},
		{"an archived document", "archived", "published", fixture.newDocument()},
		{"a trashed document", "trashed", "published", fixture.newDocument()},
	}
	fixture.apply(fixture.upsertRequest(documentSpec{
		DocumentID:      live,
		OwnerSpaceID:    space,
		Title:           "Active " + token,
		Content:         "body " + token,
		AllowedSpaceIDs: []string{space},
	}))
	for _, spec := range specs {
		fixture.apply(fixture.upsertRequest(documentSpec{
			DocumentID:        spec.documentID,
			OwnerSpaceID:      space,
			Title:             spec.name + " " + token,
			Content:           "body " + token,
			AllowedSpaceIDs:   []string{space},
			LifecycleStatus:   spec.lifecycleStatus,
			PublicationStatus: spec.publicationStatus,
		}))
	}

	// A deleted document is applied and then retired, exactly as the consumer
	// would: it must not come back through the query.
	deleted := fixture.newDocument()
	fixture.apply(fixture.upsertRequest(documentSpec{
		DocumentID:      deleted,
		OwnerSpaceID:    space,
		Title:           "Deleted " + token,
		Content:         "body " + token,
		AllowedSpaceIDs: []string{space},
	}))
	fixture.apply(fixture.deleteRequest(deleted, 9, 9))

	granted := subjectCapability("web:user:lifecycle", true, []string{space}, nil)
	response := fixture.search(granted, searchPage(token, nil, nil, 1, 20, false))
	if response.GetTotal() != 1 || !hasHit(response, live) {
		t.Fatalf("the answer contains %v (total %d), want only the active published version",
			hitDocuments(response), response.GetTotal())
	}
	for _, spec := range specs {
		if hasHit(response, spec.documentID) {
			t.Fatalf("%s is still searchable", spec.name)
		}
	}
	if hasHit(response, deleted) {
		t.Fatal("a deleted document is still searchable")
	}
}

// ---------------------------------------------------------------------------
// 16. hits carry the fields a Web surface renders
// ---------------------------------------------------------------------------

func TestIntegrationSearchHitCarriesWebFields(t *testing.T) {
	fixture := newTestFixture(t, "")
	space := fixture.newSpace()
	documentID := fixture.newDocument()
	token := "fieldtoken" + dbtest.RunTag()
	owner := "web:user:fields-" + dbtest.RunTag()
	created := time.Date(2026, 5, 4, 3, 2, 1, 123456000, time.UTC)
	firstUpdated := time.Date(2026, 6, 5, 4, 3, 2, 654321000, time.UTC)
	secondUpdated := time.Date(2026, 7, 6, 5, 4, 3, 111222000, time.UTC)

	fixture.apply(fixture.upsertRequest(documentSpec{
		DocumentID:          documentID,
		OwnerSpaceID:        space,
		OwnerSubjectKey:     owner,
		Title:               "Fields " + token,
		Content:             "body " + token,
		AllowedSpaceIDs:     []string{space},
		AuthenticatedPublic: true,
		CreatedAt:           created.Format(time.RFC3339Nano),
		OccurredAt:          firstUpdated.Format(time.RFC3339Nano),
	}))

	granted := subjectCapability(owner, true, []string{space}, nil)
	response := fixture.search(granted, searchPage(token, nil, nil, 1, 10, false))
	hit := findHit(t, response, documentID)
	if hit.GetOwnerSubjectKey() != owner {
		t.Fatalf("hit owner_subject_key = %q, want %q", hit.GetOwnerSubjectKey(), owner)
	}
	if !hit.GetAuthenticatedPublic() {
		t.Fatal("hit authenticated_public = false, want the stored flag")
	}
	requireInstant(t, "created_at", hit.GetCreatedAt(), created)
	// The document's update instant is the fact source's transaction time, not
	// the moment this index wrote the row.
	requireInstant(t, "updated_at", hit.GetUpdatedAt(), firstUpdated)

	// A later event that does not state a creation instant must not move the one
	// already stored, but its own occurred_at is the document's new update time.
	fixture.apply(fixture.upsertRequest(documentSpec{
		DocumentID:          documentID,
		OwnerSpaceID:        space,
		OwnerSubjectKey:     owner,
		Title:               "Fields updated " + token,
		Content:             "body " + token,
		AllowedSpaceIDs:     []string{space},
		AuthenticatedPublic: true,
		AggregateRevision:   2,
		ActivationRevision:  2,
		LifecycleRevision:   2,
		OccurredAt:          secondUpdated.Format(time.RFC3339Nano),
	}))
	updated := fixture.search(granted, searchPage(token, nil, nil, 1, 10, false))
	hit = findHit(t, updated, documentID)
	requireInstant(t, "created_at after the update", hit.GetCreatedAt(), created)
	requireInstant(t, "updated_at after the update", hit.GetUpdatedAt(), secondUpdated)

	// Both instants survive an independent rebuild: the applied event log stores
	// the event verbatim, so the replay path writes the same projection back.
	fixture.exec(`DELETE FROM document_search.document_index WHERE document_id = $1::text::uuid`, documentID)
	if _, err := fixture.service.Rebuild(fixture.ctx, true); err != nil {
		t.Fatalf("rebuild after deleting the projection: %v", err)
	}
	rebuilt := fixture.search(granted, searchPage(token, nil, nil, 1, 10, false))
	hit = findHit(t, rebuilt, documentID)
	requireInstant(t, "created_at after the rebuild", hit.GetCreatedAt(), created)
	requireInstant(t, "updated_at after the rebuild", hit.GetUpdatedAt(), secondUpdated)
}

// requireInstant parses a hit timestamp and requires it to be exactly the
// instant the fact source stated.
func requireInstant(t *testing.T, what, got string, want time.Time) {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, got)
	if err != nil {
		t.Fatalf("hit %s %q is not an RFC3339 instant: %v", what, got, err)
	}
	if !parsed.Equal(want) {
		t.Fatalf("hit %s = %s, want the instant the event stated (%s)", what, parsed, want)
	}
}
