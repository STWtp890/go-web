package application

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"document-search/internal/dbtest"
)

// The fixed evaluation corpus, owned by document-search.
//
// deployments/evaluation/document-search-v1.json is the retrieval gate this
// service took over. The test below is the takeover: it indexes exactly that
// corpus through the real event path and runs exactly those queries through the
// hybrid query path, then requires the answer to be reproducible.
//
// What is asserted here is reproducibility, not retrieval quality: the same
// corpus and the same queries must produce the same ordered answer before and
// after a rebuild, on every run. Semantic models and ranking effect are a
// separate concern with a separate acceptance. The readable output - which
// document each query ranked where, and which arm found it - is logged and
// recorded in the stage B evidence file.

// evaluationDataset is the dataset's schema.
type evaluationDataset struct {
	Name      string `json:"name"`
	Documents []struct {
		Key     string `json:"key"`
		Title   string `json:"title"`
		Content string `json:"content"`
	} `json:"documents"`
	Queries []struct {
		ID       string   `json:"id"`
		Category string   `json:"category"`
		Text     string   `json:"text"`
		Relevant []string `json:"relevant"`
	} `json:"queries"`
}

// evaluationDatasetPath resolves the dataset from the package directory. The
// file is the fixed input of this test, so a missing one fails rather than
// skipping: a "green" run without the corpus would prove nothing.
func evaluationDatasetPath() string {
	return filepath.Join("..", "..", "..", "..", "deployments", "evaluation", "document-search-v1.json")
}

func loadEvaluationDataset(t *testing.T) evaluationDataset {
	t.Helper()
	raw, err := os.ReadFile(evaluationDatasetPath())
	if err != nil {
		t.Fatalf("document-search evaluation: read the fixed dataset %s: %v", evaluationDatasetPath(), err)
	}
	var dataset evaluationDataset
	if err := json.Unmarshal(raw, &dataset); err != nil {
		t.Fatalf("document-search evaluation: parse the fixed dataset: %v", err)
	}
	if len(dataset.Documents) == 0 || len(dataset.Queries) == 0 {
		t.Fatalf("document-search evaluation: the dataset has %d documents and %d queries",
			len(dataset.Documents), len(dataset.Queries))
	}
	return dataset
}

func TestIntegrationEvaluationDatasetIsStableAndReproducible(t *testing.T) {
	fixture := newTestFixture(t, "")
	dataset := loadEvaluationDataset(t)
	space := fixture.newSpace()
	subject := "web:user:evaluation-" + dbtest.RunTag()
	capability := subjectCapability(subject, false, []string{space}, nil)

	documentKeys := make(map[string]string, len(dataset.Documents))
	for _, document := range dataset.Documents {
		documentID := fixture.newDocument()
		documentKeys[documentID] = document.Key
		// Every indexed field comes from the dataset: the fixture's generated
		// summary would add a per-process token to the vectorized text, and a
		// corpus whose content changes between runs could not show that the answer
		// is reproducible.
		fixture.apply(fixture.upsertRequest(documentSpec{
			DocumentID:      documentID,
			OwnerSpaceID:    space,
			OwnerSubjectKey: subject,
			Title:           document.Title,
			Summary:         document.Title,
			Content:         document.Content,
			AllowedSpaceIDs: []string{space},
		}))
	}

	// run walks the whole query set and returns the ordered document keys of each
	// answer, which is the shape the dataset is about.
	run := func() map[string][]string {
		answers := make(map[string][]string, len(dataset.Queries))
		for _, query := range dataset.Queries {
			response := fixture.search(capability, searchPage(query.Text, []string{space}, nil, 1, 20, false))
			keys := make([]string, 0, len(response.GetHits()))
			for _, hit := range response.GetHits() {
				keys = append(keys, documentKeys[hit.GetDocumentId()])
			}
			answers[query.ID] = keys
		}
		return answers
	}

	first := run()
	second := run()
	for _, query := range dataset.Queries {
		if !equalStrings(first[query.ID], second[query.ID]) {
			t.Fatalf("query %s returned %v on the first run and %v on the second", query.ID, first[query.ID], second[query.ID])
		}
	}

	// A rebuild reproduces the same answers from the locally stored events: the
	// embedding is a pure function and the collection is replaced document by
	// document, so nothing about the answer depends on when it was written.
	rebuilt, err := fixture.service.Rebuild(fixture.ctx, true)
	if err != nil {
		t.Fatalf("document-search evaluation: rebuild before the third run: %v", err)
	}
	if rebuilt.GetDocumentsFailed() != 0 {
		t.Fatalf("document-search evaluation: the rebuild reported %d failed events", rebuilt.GetDocumentsFailed())
	}
	third := run()
	for _, query := range dataset.Queries {
		if !equalStrings(first[query.ID], third[query.ID]) {
			t.Fatalf("query %s returned %v before the rebuild and %v after it", query.ID, first[query.ID], third[query.ID])
		}
	}

	// The corpus is answered as a whole: every document the dataset marks
	// relevant is in its query's answer. This is a floor for the flow (the answer
	// contains the material), not a ranking assertion.
	for _, query := range dataset.Queries {
		for _, relevant := range query.Relevant {
			if !containsString(third[query.ID], relevant) {
				t.Errorf("query %s (%s) did not answer with the relevant document %q: %v",
					query.ID, query.Category, relevant, third[query.ID])
			}
		}
	}

	// The readable result, written to the evidence file. A hit is marked "kw" when
	// the keyword arm matches it on its own and "vec" when only the vector arm
	// recalled it, so the record shows which half of the index answered.
	t.Logf("dataset %s: %d documents, %d queries, all three runs identical", dataset.Name, len(dataset.Documents), len(dataset.Queries))
	keywordOnly := fixture.keywordOnlyService(t)
	for _, query := range dataset.Queries {
		response := fixture.search(capability, searchPage(query.Text, []string{space}, nil, 1, 20, false))
		keywordResponse, err := keywordOnly.Search(fixture.ctx, nil, capability, searchPage(query.Text, []string{space}, nil, 1, 20, false))
		if err != nil {
			t.Fatalf("document-search evaluation: keyword-only run of %s: %v", query.ID, err)
		}
		keywordHits := make(map[string]struct{}, len(keywordResponse.GetHits()))
		for _, hit := range keywordResponse.GetHits() {
			keywordHits[hit.GetDocumentId()] = struct{}{}
		}
		order := make([]string, 0, len(response.GetHits()))
		for _, hit := range response.GetHits() {
			arm := "vec"
			if _, matched := keywordHits[hit.GetDocumentId()]; matched {
				arm = "kw"
			}
			order = append(order, documentKeys[hit.GetDocumentId()]+"["+arm+"]")
		}
		t.Logf("query %-14s %-22s %-34q | total=%d truncated=%v | keyword_matches=%d | order=%v | relevant=%v",
			query.ID, query.Category, query.Text, response.GetTotal(), response.GetTruncated(),
			len(keywordResponse.GetHits()), order, query.Relevant)
	}
}

// containsString reports whether a list holds a value.
func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
