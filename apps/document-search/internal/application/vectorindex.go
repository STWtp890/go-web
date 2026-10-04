package application

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"document-search/internal/config"
	"document-search/internal/infrastructure/postgres"
	"document-search/internal/infrastructure/qdrant"
	"document-search/internal/vectorindex"
)

// The vector half of the index.
//
// The keyword index and the vector collection hold the same derived documents
// and are written by the same event flow, but only the keyword half lives in the
// same database as the consumer cursor. Qdrant has no transaction shared with
// PostgreSQL, so the two are ordered deliberately: the SQL projection and its
// cursor commit together, and the vector write happens inside that window,
// before the commit. A vector failure therefore rolls the SQL side back and the
// event is redelivered; a vector write that survived a failed commit is inert,
// because every answer is re-filtered against the committed SQL projection.
//
// The collection identity is not invented here. document_search.index_generations
// is the record of which alias, storage domain, embedding profile and dimensions
// this service's index generation uses, and this file reads that row rather than
// trusting a second copy of the naming in code.
//
// The port types live in internal/vectorindex so the storage implementation and
// this package share one definition without either importing the other.

// VectorChunk is one vectorized piece of a document.
type VectorChunk = vectorindex.Chunk

// VectorDocument is one document's complete vector state.
type VectorDocument = vectorindex.Document

// VectorFilter narrows a vector query to the documents the caller may see.
type VectorFilter = vectorindex.Filter

// VectorCandidate is one recalled document.
type VectorCandidate = vectorindex.Candidate

// VectorIndex is the vector collection this service owns.
type VectorIndex = vectorindex.Index

// ErrVectorIndex reports an unusable vector collection. It is retryable: the
// consumer reconnects from its durable cursor and the query path tells the caller
// the service cannot answer rather than answering from half an index.
var ErrVectorIndex = errors.New("document-search: the vector index is unavailable")

// activeGeneration is the index generation this process serves.
type activeGeneration struct {
	Generation      string
	CollectionAlias string
	StorageDomain   string
	VectorProfile   string
	Dimensions      int
}

// NewVectorIndex opens the vector collection of the active index generation.
//
// It is the composition helper the process root and the integration fixtures
// share, so both open exactly the same collection. It returns nil when the vector
// flow is disabled, which is the one case where a keyword-only service is the
// intended configuration rather than a partial failure.
func NewVectorIndex(ctx context.Context, cfg config.Config, pool *postgres.Pool) (VectorIndex, error) {
	settings, err := cfg.VectorConfig()
	if err != nil {
		return nil, err
	}
	if !settings.Enabled {
		return nil, nil
	}
	if pool == nil || pool.Pgx() == nil {
		return nil, fmt.Errorf("%w: database pool is not initialized", ErrDatabase)
	}
	if profileDimensions(settings.Profile) == 0 {
		return nil, fmt.Errorf("document-search vector: embedding profile %q is not implemented by this build", settings.Profile)
	}

	generation, err := readActiveGeneration(ctx, pool)
	if err != nil {
		return nil, err
	}
	// The row is the authority for what the collection is; the configuration is
	// what an operator edits. A disagreement means the process would serve one
	// index while the record names another, so it fails here rather than opening
	// the wrong collection.
	if generation.CollectionAlias != strings.TrimSpace(cfg.Index.CollectionAlias) {
		return nil, fmt.Errorf(
			"document-search vector: the active generation %q records alias %q while index.collection_alias is %q; they must agree",
			generation.Generation, generation.CollectionAlias, cfg.Index.CollectionAlias,
		)
	}
	if generation.StorageDomain != strings.TrimSpace(cfg.Index.StorageDomain) {
		return nil, fmt.Errorf(
			"document-search vector: the active generation %q records storage domain %q while index.storage_domain is %q; they must agree",
			generation.Generation, generation.StorageDomain, cfg.Index.StorageDomain,
		)
	}
	if generation.VectorProfile != settings.Profile {
		return nil, fmt.Errorf(
			"document-search vector: the active generation %q was written with embedding profile %q while vector.profile is %q; "+
				"a profile change needs a new index generation, not a reinterpretation of the stored vectors",
			generation.Generation, generation.VectorProfile, settings.Profile,
		)
	}
	if generation.Dimensions != settings.Dimensions {
		return nil, fmt.Errorf(
			"document-search vector: the active generation %q has %d dimensions while vector.dimensions is %d",
			generation.Generation, generation.Dimensions, settings.Dimensions,
		)
	}

	store, err := qdrant.New(qdrant.Config{
		Host:           settings.Host,
		Port:           settings.Port,
		APIKey:         settings.APIKey,
		UseTLS:         settings.UseTLS,
		Alias:          generation.CollectionAlias,
		Generation:     generation.Generation,
		StorageDomain:  generation.StorageDomain,
		VectorProfile:  generation.VectorProfile,
		Dimensions:     uint64(generation.Dimensions),
		Timeout:        settings.Timeout,
		RequestTimeout: settings.Timeout,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrVectorIndex, err)
	}
	if err := store.EnsureGeneration(ctx); err != nil {
		_ = store.Close()
		return nil, fmt.Errorf("%w: %v", ErrVectorIndex, err)
	}
	return store, nil
}

// readActiveGeneration reads the row that names this service's live collection.
func readActiveGeneration(ctx context.Context, pool *postgres.Pool) (activeGeneration, error) {
	var generation activeGeneration
	err := pool.Pgx().QueryRow(ctx, selectActiveGenerationSQL).Scan(
		&generation.Generation,
		&generation.CollectionAlias,
		&generation.StorageDomain,
		&generation.VectorProfile,
		&generation.Dimensions,
	)
	if err != nil {
		return activeGeneration{}, fmt.Errorf(
			"%w: read the active index generation from document_search.index_generations: %v", ErrDatabase, err)
	}
	if strings.TrimSpace(generation.Generation) == "" {
		return activeGeneration{}, fmt.Errorf("%w: the active index generation has no label", ErrDatabase)
	}
	return generation, nil
}

// referencedCollections lists the physical collections every recorded generation
// names. It is the keep-set the rebuild passes to the vector index, so pruning
// can never drop a collection the record still points at.
func referencedCollections(ctx context.Context, pool *postgres.Pool) ([]string, error) {
	rows, err := pool.Pgx().Query(ctx, selectGenerationCollectionsSQL)
	if err != nil {
		return nil, fmt.Errorf("%w: read the recorded index generations: %v", ErrDatabase, err)
	}
	defer rows.Close()
	collections := make([]string, 0, 4)
	for rows.Next() {
		var alias, generation string
		if err := rows.Scan(&alias, &generation); err != nil {
			return nil, fmt.Errorf("%w: read a recorded index generation: %v", ErrDatabase, err)
		}
		collections = append(collections, qdrant.GenerationCollection(alias, generation))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%w: read the recorded index generations: %v", ErrDatabase, err)
	}
	return collections, nil
}

const selectActiveGenerationSQL = `
SELECT generation, collection_alias, storage_domain, vector_profile, vector_dimensions
FROM document_search.index_generations
WHERE active
ORDER BY generation
LIMIT 1`

const selectGenerationCollectionsSQL = `
SELECT collection_alias, generation
FROM document_search.index_generations
ORDER BY generation`
