package application

import "testing"

// TestIntegrationReviewProbePastCandidateCap is the acceptance probe for F02
// kept verbatim (gofmt only): three matches, a keyword candidate bound of two,
// the vector arm switched off, and the third page holding one document per page.
// It is the exact input the review reported, so it stays in the suite next to
// the wider walk of the same boundary
// (TestIntegrationSearchPagePastFusionWindowIsReadable, which also checks the
// other pages, the totals and the page past the end).
//
// The configured bound is the RRF fusion window. It may decide the ranking of
// the answer; it must never decide how much of the answer is readable.
func TestIntegrationReviewProbePastCandidateCap(t *testing.T) {
	fixture := newTestFixture(t, "")
	space := fixture.newSpace()
	subject := "review:pagination:" + space
	for i := 0; i < 3; i++ {
		fixture.apply(fixture.upsertRequest(documentSpec{DocumentID: fixture.newDocument(), OwnerSpaceID: space, OwnerSubjectKey: subject, Title: "reviewcapmarker", Content: "reviewcapmarker", AllowedSpaceIDs: []string{space}}))
	}
	fixture.service.cfg.Vector.Enabled = false
	fixture.service.cfg.Vector.MaxKeywordCandidates = 2
	response, err := fixture.service.searchDocuments(fixture.ctx, subjectCapability(subject, false, []string{space}, nil), searchPage("reviewcapmarker", nil, nil, 3, 1, true))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("page 3 with 3 matches and candidate cap 2: total=%d hits=%d truncated=%v", response.GetTotal(), len(response.GetHits()), response.GetTruncated())
	if response.GetTotal() != 3 || len(response.GetHits()) != 1 {
		t.Fatalf("third keyword match must remain pageable: total=%d hits=%d", response.GetTotal(), len(response.GetHits()))
	}
}
