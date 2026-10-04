package application

import (
	"fmt"
	"strings"
	"testing"

	"document-search/internal/dbtest"

	documentsearchv1 "packages/gen/documentsearch/v1"
	"packages/serviceauth"
)

// The tests in this file are the F02 regression suite: the fusion window bounds
// how deep the vector arm can reach into the keyword ranking, and it must never
// bound what a caller can page through. They run against the real PostgreSQL and
// the real Qdrant like every other integration test in this package; nothing is
// skipped.

// keywordOnlyWithWindow reconfigures the fixture's live service as the
// keyword-only index with a narrowed fusion window. It is the configuration the
// acceptance probe used: three matches, a window of two, the vector arm off.
func (fixture *testFixture) keywordOnlyWithWindow(t *testing.T, window int) {
	t.Helper()
	fixture.service.cfg.Vector.Enabled = false
	fixture.service.cfg.Vector.MaxKeywordCandidates = window
}

// hybridWithBounds reconfigures the fixture's live service as the hybrid index
// with the given fusion window and recall.
func (fixture *testFixture) hybridWithBounds(t *testing.T, window, recall int) {
	t.Helper()
	fixture.service.cfg.Vector.Enabled = true
	fixture.service.cfg.Vector.MaxKeywordCandidates = window
	fixture.service.cfg.Vector.Recall = recall
}

// pageThrough walks the answer one page at a time and returns the documents in
// order, asserting on the way that the total is the same on every page, that a
// page is full while results remain, that no document is served twice, and that
// the truncation flag means "more results follow this page".
func pageThrough(
	t *testing.T,
	fixture *testFixture,
	capability *serviceauth.CapabilityClaims,
	build func(page int32) *documentsearchv1.SearchDocumentsRequest,
	pageSize int,
	wantTotal int,
) []string {
	t.Helper()
	served := make([]string, 0, wantTotal)
	seen := make(map[string]struct{}, wantTotal)
	for page := int32(1); page <= 64; page++ {
		response := fixture.search(capability, build(page))
		if response.GetTotal() != int32(wantTotal) {
			t.Fatalf("page %d reports total %d, want the real count %d", page, response.GetTotal(), wantTotal)
		}
		if len(response.GetHits()) == 0 {
			if len(served) < wantTotal {
				t.Fatalf("page %d is empty although only %d of the %d results were served", page, len(served), wantTotal)
			}
			if response.GetTruncated() {
				t.Fatalf("the page after the last result (%d) reported truncated=true", page)
			}
			return served
		}
		if len(response.GetHits()) > pageSize {
			t.Fatalf("page %d returned %d hits, more than the page size %d", page, len(response.GetHits()), pageSize)
		}
		if wantMore := len(served)+len(response.GetHits()) < wantTotal; response.GetTruncated() != wantMore {
			t.Fatalf("page %d truncated = %v, want %v (served %d of %d)",
				page, response.GetTruncated(), wantMore, len(served)+len(response.GetHits()), wantTotal)
		}
		if wantMore := len(served)+len(response.GetHits()) < wantTotal; wantMore && len(response.GetHits()) != pageSize {
			t.Fatalf("page %d returned %d hits while %d results remain, want a full page of %d",
				page, len(response.GetHits()), wantTotal-len(served), pageSize)
		}
		for _, hit := range response.GetHits() {
			if _, duplicate := seen[hit.GetDocumentId()]; duplicate {
				t.Fatalf("document %s was served twice while paging", hit.GetDocumentId())
			}
			seen[hit.GetDocumentId()] = struct{}{}
			served = append(served, hit.GetDocumentId())
		}
	}
	t.Fatalf("paging did not terminate after 64 pages")
	return nil
}

// requireSameDocuments requires the served documents to be exactly the expected
// set, each once.
func requireSameDocuments(t *testing.T, what string, served, want []string) {
	t.Helper()
	if len(served) != len(want) {
		t.Fatalf("%s served %v, want %v", what, served, want)
	}
	counted := make(map[string]int, len(served))
	for _, documentID := range served {
		counted[documentID]++
	}
	for _, documentID := range want {
		if counted[documentID] != 1 {
			t.Fatalf("%s served %v, want %v exactly once (%s is missing or repeated)", what, served, want, documentID)
		}
	}
}

// ---------------------------------------------------------------------------
// 24. F02: a page behind the fusion window is still readable
// ---------------------------------------------------------------------------

// TestIntegrationSearchPagePastFusionWindowIsReadable is the acceptance probe as
// a regression test: three keyword matches, a two-document fusion window, the
// vector arm switched off, one document per page. Page 3 must return the third
// match and every page must report the real total.
func TestIntegrationSearchPagePastFusionWindowIsReadable(t *testing.T) {
	fixture := newTestFixture(t, "")
	space := fixture.newSpace()
	subject := "review:pagination:" + dbtest.RunTag()
	token := "reviewcapmarker" + dbtest.RunTag()
	documents := make([]string, 0, 3)
	for index := 0; index < 3; index++ {
		documentID := fixture.newDocument()
		documents = append(documents, documentID)
		fixture.apply(fixture.upsertRequest(documentSpec{
			DocumentID:      documentID,
			OwnerSpaceID:    space,
			OwnerSubjectKey: subject,
			Title:           token,
			Content:         token,
			AllowedSpaceIDs: []string{space},
		}))
	}
	fixture.keywordOnlyWithWindow(t, 2)
	capability := subjectCapability(subject, false, []string{space}, nil)

	third := fixture.search(capability, searchPage(token, nil, nil, 3, 1, true))
	if third.GetTotal() != 3 || len(third.GetHits()) != 1 {
		t.Fatalf("page 3 with 3 matches and a window of 2: total=%d hits=%d truncated=%v, want 1 hit and total 3",
			third.GetTotal(), len(third.GetHits()), third.GetTruncated())
	}
	if third.GetTruncated() {
		t.Fatal("the last page reported truncated=true")
	}

	served := pageThrough(t, fixture, capability, func(page int32) *documentsearchv1.SearchDocumentsRequest {
		return searchPage(token, nil, nil, page, 1, true)
	}, 1, 3)
	requireSameDocuments(t, "the three pages", served, documents)

	// A page past the end stays empty, and its total still describes the answer
	// rather than the page.
	beyond := fixture.search(capability, searchPage(token, nil, nil, 4, 1, true))
	if len(beyond.GetHits()) != 0 || beyond.GetTotal() != 3 || beyond.GetTruncated() {
		t.Fatalf("page 4 returned %d hits (total %d, truncated %v), want none, total 3, truncated false",
			len(beyond.GetHits()), beyond.GetTotal(), beyond.GetTruncated())
	}
}

// ---------------------------------------------------------------------------
// 25. the window size never bounds readability
// ---------------------------------------------------------------------------

// TestIntegrationSearchWindowSizeDoesNotBoundReadability walks the fusion window
// across the match count - below it, exactly at it and above it. With only the
// keyword arm ranking, the answer and its order do not depend on the window at
// all; the window only decides where the RRF ranking stops.
func TestIntegrationSearchWindowSizeDoesNotBoundReadability(t *testing.T) {
	fixture := newTestFixture(t, "")
	space := fixture.newSpace()
	token := "windowsize" + dbtest.RunTag()
	const matches = 3
	documents := make([]string, 0, matches)
	for index := 0; index < matches; index++ {
		documentID := fixture.newDocument()
		documents = append(documents, documentID)
		fixture.apply(fixture.upsertRequest(documentSpec{
			DocumentID:      documentID,
			OwnerSpaceID:    space,
			Title:           fmt.Sprintf("Window %d %s", index, token),
			Content:         "body " + token,
			AllowedSpaceIDs: []string{space},
		}))
	}
	capability := subjectCapability("web:user:window-size", false, []string{space}, nil)

	var baseline []string
	for _, window := range []int{1, 2, matches, matches + 5} {
		fixture.keywordOnlyWithWindow(t, window)
		served := pageThrough(t, fixture, capability, func(page int32) *documentsearchv1.SearchDocumentsRequest {
			return searchPage(token, []string{space}, nil, page, 1, false)
		}, 1, matches)
		requireSameDocuments(t, fmt.Sprintf("the pages with a window of %d", window), served, documents)
		if baseline == nil {
			baseline = served
			continue
		}
		if !equalStrings(served, baseline) {
			t.Fatalf("a window of %d answered in the order %v, want the keyword order %v: the window must not change the answer when only one arm ranks",
				window, served, baseline)
		}
	}
}

// ---------------------------------------------------------------------------
// 26. the paging definition does not depend on the page size
// ---------------------------------------------------------------------------

// TestIntegrationSearchPaginationIsTheSameUnderAnyPageSize walks the same
// two-document window with four page sizes and requires every walk to serve the
// same answer, in the same order, with the same total.
func TestIntegrationSearchPaginationIsTheSameUnderAnyPageSize(t *testing.T) {
	fixture := newTestFixture(t, "")
	space := fixture.newSpace()
	token := "pagesize" + dbtest.RunTag()
	const matches = 3
	documents := make([]string, 0, matches)
	for index := 0; index < matches; index++ {
		documentID := fixture.newDocument()
		documents = append(documents, documentID)
		fixture.apply(fixture.upsertRequest(documentSpec{
			DocumentID:      documentID,
			OwnerSpaceID:    space,
			Title:           fmt.Sprintf("Page size %d %s", index, token),
			Content:         "body " + token,
			AllowedSpaceIDs: []string{space},
		}))
	}
	fixture.keywordOnlyWithWindow(t, 2)
	capability := subjectCapability("web:user:page-size", false, []string{space}, nil)

	var baseline []string
	for _, pageSize := range []int{1, 2, 3, matches + 2} {
		served := pageThrough(t, fixture, capability, func(page int32) *documentsearchv1.SearchDocumentsRequest {
			return searchPage(token, []string{space}, nil, page, int32(pageSize), false)
		}, pageSize, matches)
		requireSameDocuments(t, fmt.Sprintf("the pages of size %d", pageSize), served, documents)
		if baseline == nil {
			baseline = served
			continue
		}
		if !equalStrings(served, baseline) {
			t.Fatalf("page size %d served %v, want the same order as page size 1 (%v)", pageSize, served, baseline)
		}
	}
}

// ---------------------------------------------------------------------------
// 27. the vector arm still changes the order inside the window
// ---------------------------------------------------------------------------

// TestIntegrationSearchVectorArmReordersInsideTheFusionWindow pins what the
// window is for: a recall can move a document up inside it. The keyword arm
// ranks the title match first; the vector arm recalls the document whose content
// is the query, so the fused order starts with the recalled one - while the
// answer set stays the same two documents.
func TestIntegrationSearchVectorArmReordersInsideTheFusionWindow(t *testing.T) {
	fixture := newTestFixture(t, "")
	space := fixture.newSpace()
	query := "goroutine channel"
	titleDocument := fixture.newDocument()
	recalledDocument := fixture.newDocument()

	// The title is the query and the content is unrelated filler, so this
	// document wins the keyword ranking (title weight plus the title bonus) while
	// its embedding is dominated by the filler: its closest chunk sits at
	// cosine 0.03 to the query, against 0.65 for the document below.
	fixture.apply(fixture.upsertRequest(documentSpec{
		DocumentID:      titleDocument,
		OwnerSpaceID:    space,
		Title:           query,
		Summary:         "notes",
		Content:         strings.Repeat("lorem ipsum dolor sit amet ", 88),
		AllowedSpaceIDs: []string{space},
	}))
	// The content is exactly the query, so the vector arm recalls this document
	// first even though the keyword arm ranks it second: the title bonus is
	// missing.
	fixture.apply(fixture.upsertRequest(documentSpec{
		DocumentID:      recalledDocument,
		OwnerSpaceID:    space,
		Title:           "Scheduling",
		Summary:         "runtime",
		Content:         query,
		AllowedSpaceIDs: []string{space},
	}))
	capability := subjectCapability("web:user:fusion-order", false, []string{space}, nil)
	request := searchPage(query, []string{space}, nil, 1, 10, false)

	// The keyword-only configuration is the control: the title match is first.
	fixture.keywordOnlyWithWindow(t, 2)
	keywordOnly := fixture.search(capability, request)
	if keywordOnly.GetTotal() != 2 || len(keywordOnly.GetHits()) != 2 {
		t.Fatalf("the keyword-only answer is %v (total %d), want both matches",
			hitDocuments(keywordOnly), keywordOnly.GetTotal())
	}
	if keywordOnly.GetHits()[0].GetDocumentId() != titleDocument {
		t.Fatalf("the keyword-only answer starts with %s, want the title match %s",
			keywordOnly.GetHits()[0].GetDocumentId(), titleDocument)
	}

	// One recall, a window that covers both matches: the recalled document takes
	// the first position without adding a document to the answer.
	fixture.hybridWithBounds(t, 2, 1)
	hybrid := fixture.search(capability, request)
	if hybrid.GetTotal() != 2 {
		t.Fatalf("hybrid total = %d (%v), want the same two matches: a document both arms found is one answer",
			hybrid.GetTotal(), hitDocuments(hybrid))
	}
	if len(hybrid.GetHits()) != 2 {
		t.Fatalf("the hybrid page returned %d hits, want both matches (%v)", len(hybrid.GetHits()), hitDocuments(hybrid))
	}
	if hybrid.GetHits()[0].GetDocumentId() != recalledDocument {
		t.Fatalf("the fused order starts with %s, want the recalled document %s: the vector arm must be able to reorder the window",
			hybrid.GetHits()[0].GetDocumentId(), recalledDocument)
	}
	if hybrid.GetTruncated() {
		t.Fatal("a two-document answer in a two-document window reported truncated")
	}
}

// ---------------------------------------------------------------------------
// 28. a document both arms found is one answer, and the tail stays pageable
// ---------------------------------------------------------------------------

// TestIntegrationSearchHybridPaginationDeduplicatesBothArms needs no control
// over which document is recalled: every match is a keyword match, so whichever
// one the vector arm recalls, the total must count it once and the pages must
// serve it once.
func TestIntegrationSearchHybridPaginationDeduplicatesBothArms(t *testing.T) {
	fixture := newTestFixture(t, "")
	space := fixture.newSpace()
	token := "hybriddedupe" + dbtest.RunTag()
	const matches = 3
	documents := make([]string, 0, matches)
	for index := 0; index < matches; index++ {
		documentID := fixture.newDocument()
		documents = append(documents, documentID)
		fixture.apply(fixture.upsertRequest(documentSpec{
			DocumentID:      documentID,
			OwnerSpaceID:    space,
			Title:           fmt.Sprintf("Dedupe %d %s", index, token),
			Content:         "body " + token,
			AllowedSpaceIDs: []string{space},
		}))
	}
	// A window narrower than the match count and a recall that reaches every
	// point: the window holds the keyword matches and the recalls at once.
	fixture.hybridWithBounds(t, 2, 10)
	capability := subjectCapability("web:user:hybrid-dedupe", false, []string{space}, nil)

	served := pageThrough(t, fixture, capability, func(page int32) *documentsearchv1.SearchDocumentsRequest {
		return searchPage(token, []string{space}, nil, page, 1, false)
	}, 1, matches)
	requireSameDocuments(t, "the hybrid pages", served, documents)
}

// TestIntegrationSearchHybridTailIsPageable leaves the tail behind a hybrid
// window: four matches, a two-document window and one recall. The recall is a
// keyword match, so the total must stay the keyword count, and the two tail
// documents must still be served.
func TestIntegrationSearchHybridTailIsPageable(t *testing.T) {
	fixture := newTestFixture(t, "")
	space := fixture.newSpace()
	token := "hybridtail" + dbtest.RunTag()
	const matches = 4
	documents := make([]string, 0, matches)
	for index := 0; index < matches; index++ {
		documentID := fixture.newDocument()
		documents = append(documents, documentID)
		fixture.apply(fixture.upsertRequest(documentSpec{
			DocumentID:      documentID,
			OwnerSpaceID:    space,
			Title:           fmt.Sprintf("Tail %d %s", index, token),
			Content:         "body " + token,
			AllowedSpaceIDs: []string{space},
		}))
	}
	fixture.hybridWithBounds(t, 2, 1)
	capability := subjectCapability("web:user:hybrid-tail", false, []string{space}, nil)

	served := pageThrough(t, fixture, capability, func(page int32) *documentsearchv1.SearchDocumentsRequest {
		return searchPage(token, []string{space}, nil, page, 1, false)
	}, 1, matches)
	requireSameDocuments(t, "the hybrid pages behind the window", served, documents)
}

// ---------------------------------------------------------------------------
// 29. the windowed pages keep every filter of Stage A/B
// ---------------------------------------------------------------------------

// TestIntegrationSearchWindowedPaginationKeepsScopeAndLifecycle narrows the
// fusion window to a single document and requires the owner, range, publication
// and lifecycle rules to hold across every page: the window must not become a
// way around a filter, and a filter must not become a way to lose the tail.
func TestIntegrationSearchWindowedPaginationKeepsScopeAndLifecycle(t *testing.T) {
	fixture := newTestFixture(t, "")
	spaceA := fixture.newSpace()
	spaceB := fixture.newSpace()
	subject := "web:user:window-filter-" + dbtest.RunTag()
	other := "web:user:window-other-" + dbtest.RunTag()
	token := "windowfilter" + dbtest.RunTag()

	ownDocuments := make([]string, 0, 3)
	for index := 0; index < 3; index++ {
		documentID := fixture.newDocument()
		ownDocuments = append(ownDocuments, documentID)
		fixture.apply(fixture.upsertRequest(documentSpec{
			DocumentID:      documentID,
			OwnerSpaceID:    spaceA,
			OwnerSubjectKey: subject,
			Title:           fmt.Sprintf("Own %d %s", index, token),
			Content:         "body " + token,
			AllowedSpaceIDs: []string{spaceA},
		}))
	}
	otherDocument := fixture.newDocument()
	fixture.apply(fixture.upsertRequest(documentSpec{
		DocumentID:      otherDocument,
		OwnerSpaceID:    spaceA,
		OwnerSubjectKey: other,
		Title:           "Theirs " + token,
		Content:         "body " + token,
		AllowedSpaceIDs: []string{spaceA},
	}))
	outsideDocument := fixture.newDocument()
	fixture.apply(fixture.upsertRequest(documentSpec{
		DocumentID:      outsideDocument,
		OwnerSpaceID:    spaceB,
		OwnerSubjectKey: other,
		Title:           "Elsewhere " + token,
		Content:         "body " + token,
		AllowedSpaceIDs: []string{spaceB},
	}))
	draftDocument := fixture.newDocument()
	fixture.apply(fixture.upsertRequest(documentSpec{
		DocumentID:        draftDocument,
		OwnerSpaceID:      spaceA,
		OwnerSubjectKey:   subject,
		Title:             "Draft " + token,
		Content:           "body " + token,
		AllowedSpaceIDs:   []string{spaceA},
		PublicationStatus: "draft",
	}))
	deletedDocument := fixture.newDocument()
	fixture.apply(fixture.upsertRequest(documentSpec{
		DocumentID:      deletedDocument,
		OwnerSpaceID:    spaceA,
		OwnerSubjectKey: subject,
		Title:           "Deleted " + token,
		Content:         "body " + token,
		AllowedSpaceIDs: []string{spaceA},
	}))
	fixture.apply(fixture.deleteRequest(deletedDocument, 9, 9))

	fixture.keywordOnlyWithWindow(t, 1)
	capability := subjectCapability(subject, true, []string{spaceA, spaceB}, nil)

	// The whole granted envelope, no owner filter: the other subject's private
	// document in A and in B is readable, the draft and the deleted one are not -
	// and all five results are pageable through a one-document window.
	everything := pageThrough(t, fixture, capability, func(page int32) *documentsearchv1.SearchDocumentsRequest {
		return searchPage(token, nil, nil, page, 1, false)
	}, 1, 5)
	requireSameDocuments(t, "the granted envelope", everything,
		append(append([]string{}, ownDocuments...), otherDocument, outsideDocument))
	for _, absent := range []string{draftDocument, deletedDocument} {
		for _, served := range everything {
			if served == absent {
				t.Fatalf("a non-published or deleted document was served: %s in %v", absent, everything)
			}
		}
	}

	// The owner filter narrows the same answer to the subject's own documents,
	// and the three of them are still all pageable through the one-document
	// window.
	personal := pageThrough(t, fixture, capability, func(page int32) *documentsearchv1.SearchDocumentsRequest {
		return searchPage(token, nil, nil, page, 1, true)
	}, 1, 3)
	requireSameDocuments(t, "the personal search", personal, ownDocuments)

	// A named range is still the actual filter, on top of the window.
	outsideOnly := pageThrough(t, fixture, capability, func(page int32) *documentsearchv1.SearchDocumentsRequest {
		return searchPage(token, []string{spaceB}, nil, page, 1, false)
	}, 1, 1)
	requireSameDocuments(t, "the search in space B", outsideOnly, []string{outsideDocument})

	// Nobody in space B belongs to the subject: the fail-closed answer is empty,
	// not a page from the wider grant.
	personalOutside := pageThrough(t, fixture, capability, func(page int32) *documentsearchv1.SearchDocumentsRequest {
		return searchPage(token, []string{spaceB}, nil, page, 1, true)
	}, 1, 0)
	if len(personalOutside) != 0 {
		t.Fatalf("a personal search in space B served %v, want nothing", personalOutside)
	}
}
