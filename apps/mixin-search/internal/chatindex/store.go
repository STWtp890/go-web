// Package chatindex adapts the chat corpus to a vector collection.
//
// It is the only place where the chat control plane and the shared vector core
// meet: internal/chat must not depend on internal/rag (enforced by the
// architecture test), so this package is injected by the composition root as the
// chat corpus's indexer, projection store and searcher.
//
// The chat corpus gets its own collection and its own control projection. The
// store below filters candidates against that projection before fusion, so a
// message that is not archived, or is retracted, or belongs to a tombstoned
// conversation, never reaches the retrieval path even if its vectors are still
// physically present.
package chatindex

import (
	"context"
	"fmt"
	"sync"

	"mixin-search/internal/chat"
	"mixin-search/internal/rag"
)

// maxCandidateLimit bounds the refill search when filtering removed candidates.
const maxCandidateLimit = 800

// Store wraps a vector store with the chat corpus's control projection.
//
// It implements rag.VectorStore so the shared ingest and search workflows can
// use it unchanged, and chat.ProjectionStore so the chat reconciler can converge
// the projection. It deliberately does NOT implement rag.ControlledVectorStore:
// that interface speaks document payloads, and the chat corpus filters on its
// own vocabulary.
type Store struct {
	inner  rag.VectorStore
	domain string

	mu       sync.RWMutex
	controls map[string]chat.VectorControl
}

// NewStore wraps inner as the chat corpus's collection.
func NewStore(inner rag.VectorStore, storageDomain string) (*Store, error) {
	if inner == nil {
		return nil, fmt.Errorf("chat vector store is required")
	}
	if storageDomain == "" {
		return nil, fmt.Errorf("chat storage domain is required")
	}
	return &Store{inner: inner, domain: storageDomain, controls: make(map[string]chat.VectorControl)}, nil
}

// StorageDomain reports the projection domain this store filters on.
func (s *Store) StorageDomain() string { return s.domain }

// ReplaceDocument writes one message's chunks. The core workflow passes the
// message's storage key as the document id.
func (s *Store) ReplaceDocument(ctx context.Context, documentID string, chunks []rag.IndexedChunk) error {
	return s.inner.ReplaceDocument(ctx, documentID, chunks)
}

// Close closes the wrapped store.
func (s *Store) Close() error { return s.inner.Close() }

// SyncChatControls replaces the projection. It is a full snapshot by design: a
// control that is missing from the list is no longer visible, which is how
// retraction, unarchiving and tombstoning take effect.
func (s *Store) SyncChatControls(_ context.Context, controls []chat.VectorControl) error {
	next := make(map[string]chat.VectorControl, len(controls))
	for _, control := range controls {
		if control.StorageID == "" {
			continue
		}
		next[control.StorageID] = control
	}
	s.mu.Lock()
	s.controls = next
	s.mu.Unlock()
	return nil
}

// DenseSearch recalls dense candidates and filters them against the projection.
func (s *Store) DenseSearch(ctx context.Context, query []float64, limit int) ([]rag.ScoredChunk, error) {
	return s.search(limit, func(candidateLimit int) ([]rag.ScoredChunk, error) {
		return s.inner.DenseSearch(ctx, query, candidateLimit)
	})
}

// SparseSearch recalls sparse candidates and filters them against the projection.
func (s *Store) SparseSearch(ctx context.Context, queryTokens []string, limit int) ([]rag.ScoredChunk, error) {
	return s.search(limit, func(candidateLimit int) ([]rag.ScoredChunk, error) {
		return s.inner.SparseSearch(ctx, queryTokens, candidateLimit)
	})
}

// search filters raw candidates and refills when filtering removed some, so a
// corpus full of retracted messages does not silently lose recall.
func (s *Store) search(limit int, run func(int) ([]rag.ScoredChunk, error)) ([]rag.ScoredChunk, error) {
	if limit <= 0 {
		return nil, nil
	}
	candidateLimit := limit
	for {
		raw, err := run(candidateLimit)
		if err != nil {
			return nil, err
		}
		visible := s.visible(raw)
		exhausted := len(raw) < candidateLimit
		if len(visible) >= limit || exhausted || candidateLimit >= maxCandidateLimit {
			if len(visible) > limit {
				visible = visible[:limit]
			}
			return visible, nil
		}
		candidateLimit = min(candidateLimit*2, maxCandidateLimit)
	}
}

func (s *Store) visible(raw []rag.ScoredChunk) []rag.ScoredChunk {
	s.mu.RLock()
	defer s.mu.RUnlock()
	visible := make([]rag.ScoredChunk, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, candidate := range raw {
		storageID := candidate.Chunk.DocumentID
		if _, duplicate := seen[storageID]; duplicate {
			continue
		}
		control, ok := s.controls[storageID]
		if !ok {
			// A chunk with no control is not part of any archived message: it is
			// either an orphan or a projection that has not caught up. Neither may
			// be served.
			continue
		}
		if control.StorageDomain != s.domain || !control.Archived || control.Retracted || control.Tombstoned {
			continue
		}
		seen[storageID] = struct{}{}
		visible = append(visible, candidate)
	}
	return visible
}

// ProjectionSize reports how many controls the store currently holds. It exists
// for tests and operational checks, not for retrieval decisions.
func (s *Store) ProjectionSize() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.controls)
}
