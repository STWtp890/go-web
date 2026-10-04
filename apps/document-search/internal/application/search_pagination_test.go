package application

import (
	"fmt"
	"testing"
)

// The complete answer order is the fused window followed by the keyword tail.
// pageWindowFor maps one page onto that order, and it is a pure function, so
// every combination of page, page size and window length can be checked without
// a database - including the boundary the acceptance probe for F02 hit, where
// the page starts exactly after the fusion window.

// TestPageWindowForSlicesTheAnswerAfterTheFusedWindow is the probe: three
// keyword matches, a two-document fusion window, page 3 with one document per
// page. The page must show the first tail entry, not be empty.
func TestPageWindowForSlicesTheAnswerAfterTheFusedWindow(t *testing.T) {
	plan := pageWindowFor(2, 1, 2)
	if plan.FusedFrom != 2 || plan.FusedTo != 2 {
		t.Fatalf("fused slice = [%d,%d), want the window left untouched", plan.FusedFrom, plan.FusedTo)
	}
	if plan.TailOffset != 0 || plan.TailLimit != 1 {
		t.Fatalf("tail slice = offset %d limit %d, want the first tail entry", plan.TailOffset, plan.TailLimit)
	}
}

// TestPageWindowForMatchesTheConcatenatedAnswer compares the plan against the
// answer sliced directly, which is the definition of a page.
func TestPageWindowForMatchesTheConcatenatedAnswer(t *testing.T) {
	cases := []struct {
		name   string
		window int
		tail   int
		offset int64
		limit  int
	}{
		{"first page inside the window", 3, 2, 0, 2},
		{"last page inside the window", 3, 2, 2, 2},
		{"page spanning the window and the tail", 2, 3, 1, 2},
		{"first page after the window", 2, 3, 2, 1},
		{"deep page in the tail", 2, 10, 7, 3},
		{"tail page starting before a zero-size window", 0, 4, 0, 2},
		{"deep page with an empty window", 0, 4, 3, 2},
		{"page past the answer", 2, 3, 9, 2},
		{"window longer than the page", 10, 0, 4, 3},
		{"one page larger than the answer", 2, 3, 0, 10},
		{"zero page size means one", 2, 3, 1, 0},
		{"whole answer on one page", 3, 0, 0, 5},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			plan := pageWindowFor(testCase.offset, testCase.limit, testCase.window)
			got := pagedDocuments(plan, testCase.window, testCase.tail)
			want := servedDocuments(testCase.offset, testCase.limit, testCase.window, testCase.tail)
			if !equalStrings(got, want) {
				t.Fatalf("pageWindowFor(offset=%d, limit=%d, window=%d) served %v, want %v",
					testCase.offset, testCase.limit, testCase.window, got, want)
			}
		})
	}
}

// TestPageWindowForNeverSkipsOrRepeatsAResult is the acceptance property of the
// fix: for any page size and any window, the pages together serve the whole
// answer exactly once, in order, and a page that starts before the end of the
// answer is never empty.
func TestPageWindowForNeverSkipsOrRepeatsAResult(t *testing.T) {
	for window := 0; window <= 4; window++ {
		for tail := 0; tail <= 4; tail++ {
			for limit := 1; limit <= 3; limit++ {
				served := make([]string, 0, window+tail)
				for page := 0; page < 10; page++ {
					offset := int64(page * limit)
					got := pagedDocuments(pageWindowFor(offset, limit, window), window, tail)
					if offset < int64(window+tail) && len(got) == 0 {
						t.Fatalf("window=%d tail=%d limit=%d: page %d (offset %d) is empty although %d results remain",
							window, tail, limit, page+1, offset, window+tail-int(offset))
					}
					served = append(served, got...)
				}
				if want := allDocuments(window, tail); !equalStrings(served, want) {
					t.Fatalf("window=%d tail=%d limit=%d: the pages served %v, want the whole answer %v exactly once",
						window, tail, limit, served, want)
				}
			}
		}
	}
}

// TestFusionWindowCountsEveryAnswerOnce is the counting invariant of the answer
// as a property: the fused window plus the keyword matches the window did not
// name are exactly the keyword matches plus the recalls the keyword arm did not
// match - no gap and no double count - whatever the window width, the recall and
// the overlap between the arms are.
func TestFusionWindowCountsEveryAnswerOnce(t *testing.T) {
	for keywordTotal := 0; keywordTotal <= 6; keywordTotal++ {
		for window := 1; window <= 6; window++ {
			for vectorOnly := 0; vectorOnly <= 2; vectorOnly++ {
				for overlap := 0; overlap <= 2; overlap++ {
					keywordWindow := make([]searchRow, 0, window)
					for index := 0; index < keywordTotal && index < window; index++ {
						keywordWindow = append(keywordWindow, searchRow{DocumentID: keywordID(index), KeywordMatch: true})
					}
					// The recalled keyword matches are taken from the end of the
					// ranking first, which is where the deduplication between the
					// window and the tail actually bites.
					overlaps := make([]string, 0, overlap)
					for index := keywordTotal - 1; index >= 0 && len(overlaps) < overlap; index-- {
						overlaps = append(overlaps, keywordID(index))
					}
					vectorRows := make([]searchRow, 0, len(overlaps)+vectorOnly)
					vectorOrder := make([]string, 0, len(overlaps)+vectorOnly)
					for _, documentID := range overlaps {
						vectorRows = append(vectorRows, searchRow{DocumentID: documentID, KeywordMatch: true})
						vectorOrder = append(vectorOrder, documentID)
					}
					for index := 0; index < vectorOnly; index++ {
						documentID := fmt.Sprintf("vector-only-%d", index)
						vectorRows = append(vectorRows, searchRow{DocumentID: documentID, KeywordMatch: false})
						vectorOrder = append(vectorOrder, documentID)
					}

					merged := mergeWindow(keywordWindow, vectorRows)
					if merged.VectorOnly != int64(vectorOnly) {
						t.Fatalf("keywordTotal=%d window=%d vectorOnly=%d overlap=%d: only-recall count = %d, want %d",
							keywordTotal, window, vectorOnly, overlap, merged.VectorOnly, vectorOnly)
					}
					fused := fuseRanks(merged.KeywordOrder, vectorOrder, 60)
					windowIDs := make(map[string]struct{}, len(fused))
					answer := make([]string, 0, keywordTotal+vectorOnly)
					for _, entry := range fused {
						if _, duplicate := windowIDs[entry.DocumentID]; duplicate {
							t.Fatalf("keywordTotal=%d window=%d vectorOnly=%d overlap=%d: the fused window repeats %s",
								keywordTotal, window, vectorOnly, overlap, entry.DocumentID)
						}
						if _, known := merged.Rows[entry.DocumentID]; !known {
							t.Fatalf("keywordTotal=%d window=%d vectorOnly=%d overlap=%d: no row for fused document %s",
								keywordTotal, window, vectorOnly, overlap, entry.DocumentID)
						}
						windowIDs[entry.DocumentID] = struct{}{}
						answer = append(answer, entry.DocumentID)
					}
					// The tail: the keyword matches outside the window, in
					// keyword order, minus the ones the window already holds.
					for index := window; index < keywordTotal; index++ {
						documentID := keywordID(index)
						if _, inWindow := windowIDs[documentID]; inWindow {
							continue
						}
						answer = append(answer, documentID)
					}
					if len(answer) != keywordTotal+vectorOnly {
						t.Fatalf("keywordTotal=%d window=%d vectorOnly=%d overlap=%d: the answer holds %d documents (%v), want %d",
							keywordTotal, window, vectorOnly, overlap, len(answer), answer, keywordTotal+vectorOnly)
					}
					seen := make(map[string]struct{}, len(answer))
					for _, documentID := range answer {
						if _, duplicate := seen[documentID]; duplicate {
							t.Fatalf("keywordTotal=%d window=%d vectorOnly=%d overlap=%d: %s is answered twice (%v)",
								keywordTotal, window, vectorOnly, overlap, documentID, answer)
						}
						seen[documentID] = struct{}{}
					}
				}
			}
		}
	}
}

// keywordID names the keyword match at a rank, 0 being the top of the ranking.
func keywordID(rank int) string { return fmt.Sprintf("keyword-%d", rank) }

// pagedDocuments applies a plan to an answer of "window" fused entries followed
// by "tail" keyword entries, exactly as pageHits does. A tail slice that reaches
// past the answer is shortened, which is what the LIMIT of the tail statement
// does.
func pagedDocuments(plan pageWindow, window, tail int) []string {
	answer := allDocuments(window, tail)
	served := make([]string, 0, tail+window)
	for index := plan.FusedFrom; index < plan.FusedTo; index++ {
		served = append(served, answer[index])
	}
	for index := plan.TailOffset; index < plan.TailOffset+plan.TailLimit && int64(window)+index < int64(len(answer)); index++ {
		served = append(served, answer[int64(window)+index])
	}
	return served
}

// servedDocuments slices the answer directly: the model the plan has to agree
// with, written independently of pageWindowFor.
func servedDocuments(offset int64, limit int, window, tail int) []string {
	if limit <= 0 {
		limit = 1
	}
	answer := allDocuments(window, tail)
	start := offset
	if start > int64(len(answer)) {
		start = int64(len(answer))
	}
	end := offset + int64(limit)
	if end > int64(len(answer)) {
		end = int64(len(answer))
	}
	if end < start {
		end = start
	}
	return answer[start:end]
}

// allDocuments names the answer: the fused window first, then the keyword tail.
func allDocuments(window, tail int) []string {
	answer := make([]string, 0, window+tail)
	for index := 0; index < window; index++ {
		answer = append(answer, fmt.Sprintf("window-%d", index))
	}
	for index := 0; index < tail; index++ {
		answer = append(answer, fmt.Sprintf("tail-%d", index))
	}
	return answer
}
