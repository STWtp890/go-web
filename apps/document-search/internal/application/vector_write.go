package application

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"document-search/internal/config"
)

// The vector write path.
//
// A vector write is not transactional with the SQL projection, so it is applied
// inside the same window: after the SQL mutation and before the commit. A vector
// failure rolls the SQL side back, which keeps the event pending and makes the
// redelivery apply both halves again. Both halves are idempotent - the event log
// is keyed by event_id and a document's points are replaced as a whole - so a
// retry converges instead of duplicating.
//
// A batch collects one mutation per document. Within one replay batch a document
// can appear several times (an upsert followed by its delete, for instance), and
// only the last state is the one an observer should see, which is exactly what
// the SQL fences produce as well.

// vectorBatch accumulates the vector mutations of one apply or replay batch.
type vectorBatch struct {
	index    VectorIndex
	replaces map[string]VectorDocument
	deletes  map[string]struct{}
	order    []string
}

// newVectorBatch returns an accumulator for an index, or a no-op accumulator when
// the vector flow is disabled.
func newVectorBatch(index VectorIndex) *vectorBatch {
	return &vectorBatch{
		index:    index,
		replaces: make(map[string]VectorDocument),
		deletes:  make(map[string]struct{}),
	}
}

// replace records the document's current vector state.
func (batch *vectorBatch) replace(document VectorDocument) {
	if batch == nil || batch.index == nil {
		return
	}
	if _, tracked := batch.replaces[document.DocumentID]; !tracked {
		if _, tracked := batch.deletes[document.DocumentID]; !tracked {
			batch.order = append(batch.order, document.DocumentID)
		}
	}
	delete(batch.deletes, document.DocumentID)
	batch.replaces[document.DocumentID] = document
}

// remove records that the document no longer has a vector.
func (batch *vectorBatch) remove(documentID string) {
	if batch == nil || batch.index == nil {
		return
	}
	if _, tracked := batch.replaces[documentID]; !tracked {
		if _, tracked := batch.deletes[documentID]; !tracked {
			batch.order = append(batch.order, documentID)
		}
	}
	delete(batch.replaces, documentID)
	batch.deletes[documentID] = struct{}{}
}

// pending reports whether anything has to be written.
func (batch *vectorBatch) pending() bool {
	return batch != nil && batch.index != nil && len(batch.order) > 0
}

// flush writes the accumulated mutations to the vector index.
func (batch *vectorBatch) flush(ctx context.Context) error {
	if !batch.pending() {
		return nil
	}
	deletes := make([]string, 0, len(batch.deletes))
	replaces := make([]VectorDocument, 0, len(batch.replaces))
	for _, documentID := range batch.order {
		if document, replaced := batch.replaces[documentID]; replaced {
			replaces = append(replaces, document)
			continue
		}
		deletes = append(deletes, documentID)
	}
	sort.Strings(deletes)
	if len(deletes) > 0 {
		if err := batch.index.DeleteDocuments(ctx, deletes); err != nil {
			return fmt.Errorf("%w: %v", ErrVectorIndex, err)
		}
	}
	if len(replaces) > 0 {
		if err := batch.index.ReplaceDocuments(ctx, replaces); err != nil {
			return fmt.Errorf("%w: %v", ErrVectorIndex, err)
		}
	}
	batch.replaces = make(map[string]VectorDocument)
	batch.deletes = make(map[string]struct{})
	batch.order = batch.order[:0]
	return nil
}

// configuredVectorProfile returns the embedding profile and dimensions the
// configured generation was opened with. The config validator has already
// rejected an unknown profile or a dimension mismatch, so the fallback here only
// covers a configuration assembled in code.
func configuredVectorProfile(cfg config.Config) (string, int) {
	profile := strings.TrimSpace(cfg.Vector.Profile)
	if profile == "" {
		profile = VectorProfileLocalHashV1
	}
	dimensions := profileDimensions(profile)
	if dimensions == 0 {
		dimensions = cfg.Vector.Dimensions
	}
	return profile, dimensions
}

// vectorDocumentForEvent projects one document event onto the vector collection.
//
// The document is vectorized exactly as it is chunked for the keyword index, so
// chunk_count in document_search.document_index is also the number of points the
// document has. The first chunk additionally carries the title and summary,
// which is what makes a title-only query able to recall its document.
func vectorDocumentForEvent(event *documentEvent, cfg config.Config) VectorDocument {
	profile, dimensions := configuredVectorProfile(cfg)
	document := VectorDocument{
		DocumentID:          event.DocumentID,
		VersionID:           event.VersionID,
		OwnerSpaceID:        event.OwnerSpaceID,
		OwnerSubjectKey:     event.OwnerSubjectKey,
		AuthenticatedPublic: event.AuthenticatedPublic,
		AllowedSpaceIDs:     append([]string(nil), event.AllowedSpaceIDs...),
		Title:               event.Title,
		VectorProfile:       profile,
		VectorDimensions:    dimensions,
		AggregateRevision:   event.AggregateRevision,
		AccessRevision:      event.AccessRevision,
		LifecycleRevision:   event.LifecycleRevision,
	}
	maxChunks := cfg.Index.MaxChunks
	if maxChunks <= 0 {
		maxChunks = defaultMaxChunks
	}
	chunks := chunkText(event.Content, event.IndexProfile, maxChunks)
	if len(chunks) == 0 {
		return document
	}
	whole := strings.TrimSpace(event.Title + "\n\n" + event.Summary + "\n\n" + event.Content)
	if len(tokenize(whole)) == 0 {
		// Nothing in this document carries a feature, so there is no vector to
		// store. A zero vector is not a valid cosine point and would be refused
		// by the server anyway.
		return document
	}
	document.Chunks = make([]VectorChunk, 0, len(chunks))
	for index, chunk := range chunks {
		text := chunk
		if index == 0 {
			text = event.Title + "\n\n" + event.Summary + "\n\n" + chunk
		}
		if len(tokenize(text)) == 0 {
			// Separator-only chunk: embedding the document instead of a zero
			// vector keeps the point count equal to the chunk count.
			text = whole
		}
		document.Chunks = append(document.Chunks, VectorChunk{Index: index, Vector: embedText32(text)})
	}
	return document
}
