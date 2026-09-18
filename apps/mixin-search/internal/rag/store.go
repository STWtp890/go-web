package rag

import (
	"context"
	"math"
	"sort"
	"sync"
)

// IndexedChunk is the storage representation produced by the ingest workflow.
type IndexedChunk struct {
	Chunk  Chunk
	Dense  []float64
	Terms  map[string]int
	Length int
}

// ScoredChunk is one result from either the dense or sparse recall path.
type ScoredChunk struct {
	Chunk Chunk
	Score float64
}

// VectorStore is the common boundary implemented by memory, Qdrant and
// PostgreSQL/pgvector backends.
type VectorStore interface {
	ReplaceDocument(ctx context.Context, documentID string, chunks []IndexedChunk) error
	DenseSearch(ctx context.Context, query []float64, limit int) ([]ScoredChunk, error)
	SparseSearch(ctx context.Context, queryTokens []string, limit int) ([]ScoredChunk, error)
	Close() error
}

// AliasedVectorStore is a store whose configured name is a stable alias over
// physical collections.
//
// It is what lets a corpus be rebuilt without changing what callers ask for:
// the rebuild fills a new generation and switches the alias, and reads and
// writes follow the alias atomically. Each corpus owns its own alias, so a
// switch in one corpus can never move the other's.
type AliasedVectorStore interface {
	VectorStore
	// Alias reports the stable name this store addresses.
	Alias() string
	// PhysicalCollection resolves the collection the alias currently points at.
	PhysicalCollection(ctx context.Context) (string, error)
	// PrepareGeneration creates the physical collection of a new generation
	// without switching to it.
	PrepareGeneration(ctx context.Context, generation string, dimensions uint64) (string, error)
	// SwitchAlias points the alias at another existing physical collection.
	SwitchAlias(ctx context.Context, target string) error
}

// VectorDocumentControl is the normalized control-plane projection attached to
// every vector chunk. StorageID is the opaque key used by the core workflow;
// the remaining fields are the public document contract used for filtering.
type VectorDocumentControl struct {
	StorageID           string
	StorageDomain       string
	DocumentID          string
	VersionID           string
	OwnerSpaceID        string
	AuthenticatedPublic bool
	GrantedSpaceIDs     []string
	Active              bool
	Tombstoned          bool
	ActivationRevision  uint64
	AccessRevision      uint64
	LifecycleRevision   uint64
	ContentSHA256       string
}

// VectorSearchFilter is the authorization context pushed into a capable
// vector store. Empty allow-lists deliberately mean public documents only.
type VectorSearchFilter struct {
	StorageDomain      string
	AllowedSpaceIDs    []string
	AllowedDocumentIDs []string
}

// ControlledVectorStore is implemented by production candidates that can
// materialize document controls and apply them before vector candidates are
// selected. Other stores continue to use the contract-layer fallback filter.
type ControlledVectorStore interface {
	SyncDocumentControls(ctx context.Context, controls []VectorDocumentControl) error
	DenseSearchFiltered(ctx context.Context, query []float64, limit int, filter VectorSearchFilter) ([]ScoredChunk, error)
	SparseSearchFiltered(ctx context.Context, queryTokens []string, limit int, filter VectorSearchFilter) ([]ScoredChunk, error)
}

// MemoryStore replaces Qdrant in this runnable example.
type MemoryStore struct {
	mu     sync.RWMutex
	chunks map[string]IndexedChunk
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{chunks: make(map[string]IndexedChunk)}
}

func (s *MemoryStore) ReplaceDocument(_ context.Context, documentID string, chunks []IndexedChunk) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for id, chunk := range s.chunks {
		if chunk.Chunk.DocumentID == documentID {
			delete(s.chunks, id)
		}
	}
	for _, chunk := range chunks {
		s.chunks[chunk.Chunk.ID] = chunk
	}
	return nil
}

func (s *MemoryStore) DenseSearch(_ context.Context, query []float64, limit int) ([]ScoredChunk, error) {
	chunks := s.snapshot()
	hits := make([]ScoredChunk, 0, len(chunks))
	for _, chunk := range chunks {
		score := dot(query, chunk.Dense)
		if score > 0 {
			hits = append(hits, ScoredChunk{Chunk: chunk.Chunk, Score: score})
		}
	}
	return topScored(hits, limit), nil
}

func (s *MemoryStore) SparseSearch(_ context.Context, queryTokens []string, limit int) ([]ScoredChunk, error) {
	chunks := s.snapshot()
	if len(chunks) == 0 || len(queryTokens) == 0 {
		return nil, nil
	}

	uniqueQueryTerms := make(map[string]struct{}, len(queryTokens))
	for _, token := range queryTokens {
		uniqueQueryTerms[token] = struct{}{}
	}

	documentFrequency := make(map[string]int, len(uniqueQueryTerms))
	var totalLength int
	for _, chunk := range chunks {
		totalLength += chunk.Length
		for term := range uniqueQueryTerms {
			if chunk.Terms[term] > 0 {
				documentFrequency[term]++
			}
		}
	}

	averageLength := float64(totalLength) / float64(len(chunks))
	if averageLength == 0 {
		return nil, nil
	}
	const k1, b = 1.2, 0.75
	hits := make([]ScoredChunk, 0, len(chunks))
	for _, chunk := range chunks {
		var score float64
		for term := range uniqueQueryTerms {
			tf := float64(chunk.Terms[term])
			if tf == 0 {
				continue
			}
			df := float64(documentFrequency[term])
			idf := math.Log(1 + (float64(len(chunks))-df+0.5)/(df+0.5))
			lengthRatio := float64(chunk.Length) / averageLength
			score += idf * (tf * (k1 + 1)) / (tf + k1*(1-b+b*lengthRatio))
		}
		if score > 0 {
			hits = append(hits, ScoredChunk{Chunk: chunk.Chunk, Score: score})
		}
	}
	return topScored(hits, limit), nil
}

func (s *MemoryStore) Close() error { return nil }

func (s *MemoryStore) snapshot() []IndexedChunk {
	s.mu.RLock()
	defer s.mu.RUnlock()

	chunks := make([]IndexedChunk, 0, len(s.chunks))
	for _, chunk := range s.chunks {
		chunks = append(chunks, chunk)
	}
	return chunks
}

func topScored(hits []ScoredChunk, limit int) []ScoredChunk {
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score == hits[j].Score {
			return hits[i].Chunk.ID < hits[j].Chunk.ID
		}
		return hits[i].Score > hits[j].Score
	})
	if limit > 0 && len(hits) > limit {
		return hits[:limit]
	}
	return hits
}

func dot(left, right []float64) float64 {
	limit := min(len(left), len(right))
	var result float64
	for i := 0; i < limit; i++ {
		result += left[i] * right[i]
	}
	return result
}
