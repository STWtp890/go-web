package application

import (
	"math"
	"testing"
)

// The fusion is a pure function so the merge rule is testable without a database
// or a collection - and so an ordering that depends on map iteration cannot hide
// inside a query test that happens to pass.

func TestFuseRanksPrefersDocumentsBothArmsFound(t *testing.T) {
	fused := fuseRanks([]string{"a", "b"}, []string{"b", "c"}, 60)
	order := fusedDocumentIDs(fused)
	want := []string{"b", "a", "c"}
	if !equalStrings(order, want) {
		t.Fatalf("fused order = %v, want %v: a document both arms ranked must outrank one only one arm ranked",
			order, want)
	}
	// b = keyword rank 2 + vector rank 1, a = keyword rank 1, c = vector rank 2.
	wantScore := 1.0/62.0 + 1.0/61.0
	if math.Abs(fused[0].Score-wantScore) > 1e-12 {
		t.Fatalf("b scored %v, want %v", fused[0].Score, wantScore)
	}
	if math.Abs(fused[1].Score-1.0/61.0) > 1e-12 {
		t.Fatalf("a scored %v, want %v", fused[1].Score, 1.0/61.0)
	}
	if math.Abs(fused[2].Score-1.0/62.0) > 1e-12 {
		t.Fatalf("c scored %v, want %v", fused[2].Score, 1.0/62.0)
	}
}

func TestFuseRanksKeepsTheKeywordOrderWhenTheVectorArmIsEmpty(t *testing.T) {
	keyword := []string{"first", "second", "third"}
	fused := fuseRanks(keyword, nil, 60)
	if order := fusedDocumentIDs(fused); !equalStrings(order, keyword) {
		t.Fatalf("fused order = %v, want the keyword order %v", order, keyword)
	}
}

func TestFuseRanksBreaksTiesOnTheDocumentID(t *testing.T) {
	// Two documents at the same rank in their own arms score identically.
	first := fuseRanks([]string{"bbb", "aaa"}, []string{"aaa", "bbb"}, 60)
	second := fuseRanks([]string{"aaa", "bbb"}, []string{"bbb", "aaa"}, 60)
	if order := fusedDocumentIDs(first); !equalStrings(order, []string{"aaa", "bbb"}) {
		t.Fatalf("fused order = %v, want the document id to break the tie", order)
	}
	if !equalStrings(fusedDocumentIDs(first), fusedDocumentIDs(second)) {
		t.Fatal("the same ranks produced two different orders")
	}
}

func TestFuseRanksIsStableAcrossRepeatedCalls(t *testing.T) {
	keyword := []string{"d", "b", "f", "a", "c", "e"}
	vector := []string{"e", "a", "d", "c", "b", "f"}
	baseline := fusedDocumentIDs(fuseRanks(keyword, vector, 60))
	for attempt := 0; attempt < 20; attempt++ {
		if order := fusedDocumentIDs(fuseRanks(keyword, vector, 60)); !equalStrings(order, baseline) {
			t.Fatalf("attempt %d produced %v, want the stable order %v", attempt, order, baseline)
		}
	}
}

func TestFuseRanksFallsBackToTheStandardConstant(t *testing.T) {
	fused := fuseRanks([]string{"only"}, nil, 0)
	if len(fused) != 1 {
		t.Fatalf("fused %d documents, want 1", len(fused))
	}
	if math.Abs(fused[0].Score-1.0/61.0) > 1e-12 {
		t.Fatalf("score = %v, want the 1/(60+1) fallback", fused[0].Score)
	}
}

// TestVectorFiltersNeverWidenToEverything pins the fail-closed direction of the
// recall filter: an empty family set is the empty scope, not "all documents".
func TestVectorFiltersNeverWidenToEverything(t *testing.T) {
	if !(VectorFilter{}).MatchesNothing() {
		t.Fatal("an empty filter claims to match documents")
	}
	if (VectorFilter{SpaceIDs: []string{"a"}}).MatchesNothing() {
		t.Fatal("a filter with a space claims to match nothing")
	}
	if (VectorFilter{DocumentIDs: []string{"d"}}).MatchesNothing() {
		t.Fatal("a filter with a document claims to match nothing")
	}
	if !(VectorFilter{IncludePublic: true}).MatchesNothing() {
		t.Fatal("the public floor is not a recall family: a filter with only the floor has no family to recall")
	}
	if (VectorFilter{IncludePublic: true, SpaceIDs: []string{"a"}}).MatchesNothing() {
		t.Fatal("the public floor switched off a real family")
	}
}

func fusedDocumentIDs(fused []rankedDocument) []string {
	ids := make([]string, 0, len(fused))
	for _, entry := range fused {
		ids = append(ids, entry.DocumentID)
	}
	return ids
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
