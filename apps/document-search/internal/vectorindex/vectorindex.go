// Package vectorindex is the port of the document search service's vector
// collection: the types the index half and the storage half agree on.
//
// It is deliberately dependency-free and knows nothing about Qdrant, PostgreSQL
// or the embedding function. internal/infrastructure/qdrant implements Index,
// internal/application consumes it, and the composition root opens the
// implementation recorded in document_search.index_generations.
package vectorindex

import "context"

// Chunk is one vectorized piece of a document.
type Chunk struct {
	// Index is the chunk position, which also makes the point id reproducible.
	Index int
	// Vector is the embedding of the chunk text.
	Vector []float32
}

// Document is one document's complete vector state: everything the collection
// stores about it. Replacing a document always replaces all of it, so a
// document's points are exactly the chunks of its current version.
type Document struct {
	DocumentID          string
	VersionID           string
	OwnerSpaceID        string
	OwnerSubjectKey     string
	AuthenticatedPublic bool
	AllowedSpaceIDs     []string
	Title               string
	VectorProfile       string
	VectorDimensions    int
	AggregateRevision   uint64
	AccessRevision      uint64
	LifecycleRevision   uint64
	Chunks              []Chunk
}

// Filter narrows a query to the documents a caller may see. It is derived from
// the same resolved scope the SQL predicate uses.
//
// The filter is a recall optimisation, not the authorization decision: the SQL
// scope filter is applied to every recalled document id afterwards, so a stale
// payload can only lose a candidate, never add an answer the caller may not see.
type Filter struct {
	SpaceIDs        []string
	DocumentIDs     []string
	IncludePublic   bool
	OwnerSubjectKey string
}

// MatchesNothing reports whether the filter names no family to recall.
//
// Space and document identifiers are the families a recall runs in. The
// authenticated-public floor is deliberately not one of them: it is an
// authorization statement over the whole deployment, and treating it as a recall
// family would make every un-narrowed query pull in whatever the entire public
// corpus looks like under the embedding. A caller with only the floor is served
// by the keyword arm; every recalled document is still re-checked against the
// full scope, floor included, before it can become a hit.
func (filter Filter) MatchesNothing() bool {
	return len(filter.SpaceIDs) == 0 && len(filter.DocumentIDs) == 0
}

// Candidate is one recalled document. Several points of the same document
// collapse into one candidate, keeping the best score: the answer is per
// document, not per chunk.
type Candidate struct {
	DocumentID string
	VersionID  string
	Score      float64
}

// Index is the vector collection this service owns.
type Index interface {
	// Alias, StorageDomain, Generation and Profile report the identity the
	// collection was opened with, which always comes from the active
	// index_generations row.
	Alias() string
	StorageDomain() string
	Generation() string
	Profile() string
	Dimensions() int
	// ReplaceDocuments removes every point of each document and writes the
	// document's current chunks, in that order, so a shrinking document cannot
	// leave an old chunk behind.
	ReplaceDocuments(ctx context.Context, documents []Document) error
	// DeleteDocuments removes every point of the named documents.
	DeleteDocuments(ctx context.Context, documentIDs []string) error
	// Search recalls candidates inside the filter, best score first.
	Search(ctx context.Context, vector []float32, limit int, filter Filter) ([]Candidate, error)
	// PointCount reports how many points one document currently has.
	PointCount(ctx context.Context, documentID string) (uint64, error)
	// PruneGenerations drops the physical collections of this alias that no
	// index_generations row references, and reports what it dropped.
	PruneGenerations(ctx context.Context, referenced []string) ([]string, error)
	// Health reports whether the vector backend answers.
	Health(ctx context.Context) error
	Close() error
}
