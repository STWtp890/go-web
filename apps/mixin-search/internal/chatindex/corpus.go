package chatindex

import (
	"context"
	"fmt"
	"strings"

	"mixin-search/internal/chat"
	"mixin-search/internal/rag"
)

// Default chunking for chat messages. A message is short by nature, so the chunk
// is smaller than a document's and the overlap keeps a sentence that straddles
// the boundary retrievable from both sides.
const (
	defaultChunkSize = 180
	defaultOverlap   = 20
)

// Config configures one chat corpus instance.
type Config struct {
	// VectorStore is the chat collection's store. It must be a store dedicated to
	// this corpus: the corpus's isolation comes from having its own collection,
	// not from what the store is willing to filter.
	VectorStore rag.VectorStore
	// ControlStore is the chat corpus's own control state.
	ControlStore chat.ControlStore
	// StorageDomain overrides the projection domain. It defaults to the control
	// store's domain, which is already corpus-specific.
	StorageDomain string
}

// Corpus is the chat corpus's composition: its own collection wrapper, its own
// vector core instance and its own control service.
type Corpus struct {
	store   *Store
	core    *rag.Service
	service *chat.IndexService
}

// New builds the chat corpus. The vector core is constructed over this corpus's
// store and gets this corpus's pipeline, so no document pipeline or collection
// is reachable from here.
func New(ctx context.Context, config Config) (*Corpus, error) {
	if config.ControlStore == nil {
		return nil, fmt.Errorf("chat control store is required")
	}
	domain := strings.TrimSpace(config.StorageDomain)
	if domain == "" {
		domain = config.ControlStore.StorageDomain()
	}
	store, err := NewStore(config.VectorStore, domain)
	if err != nil {
		return nil, err
	}
	core, err := rag.NewServiceWithStore(ctx, store)
	if err != nil {
		return nil, err
	}
	if err := core.RegisterDocumentPipeline(chatPipeline{}); err != nil {
		_ = core.Close()
		return nil, fmt.Errorf("register chat pipeline: %w", err)
	}
	corpus := &Corpus{store: store, core: core}
	service, err := chat.NewIndexService(ctx, chat.IndexServiceConfig{
		ControlStore: config.ControlStore,
		Indexer:      corpus,
		Projection:   store,
		Searcher:     corpus,
		// One domain for both the collection and the control plane: the store
		// filters on it, so a different value on either side would hide every
		// candidate.
		StorageDomain: domain,
	})
	if err != nil {
		_ = core.Close()
		return nil, err
	}
	corpus.service = service
	return corpus, nil
}

// Service exposes the chat control plane the transport adapter serves.
func (c *Corpus) Service() *chat.IndexService { return c.service }

// StartProjectionReconciler keeps this corpus's projection converged.
func (c *Corpus) StartProjectionReconciler(ctx context.Context) {
	c.service.StartProjectionReconciler(ctx)
}

// StorageDomain reports the corpus's projection domain.
func (c *Corpus) StorageDomain() string { return c.store.StorageDomain() }

// ProjectionSize reports how many controls the collection currently holds.
func (c *Corpus) ProjectionSize() int { return c.store.ProjectionSize() }

// Close releases the vector collection.
func (c *Corpus) Close() error { return c.core.Close() }

// IndexMessage chips and embeds one message into the chat collection.
func (c *Corpus) IndexMessage(ctx context.Context, storageID string, message chat.MessageInput) (int, error) {
	result, err := c.core.IngestDocument(ctx, rag.IngestDocumentRequest{
		DocumentID: storageID,
		Filename:   message.MessageID + chatExtension,
		Title:      message.SenderID,
		Content:    []byte(message.Content),
		ChunkSize:  defaultChunkSize,
		Overlap:    defaultOverlap,
	})
	if err != nil {
		return 0, err
	}
	return result.ChunkCount, nil
}

// DeleteMessage removes one message's vectors from the chat collection.
func (c *Corpus) DeleteMessage(ctx context.Context, storageID string) error {
	return c.core.DeleteIndexedDocument(ctx, storageID)
}

// SearchMessages recalls candidates from the chat collection. The store has
// already dropped anything the projection does not mark as retrievable;
// authorization is applied by the control plane against the same snapshot.
func (c *Corpus) SearchMessages(ctx context.Context, query string, limit int) ([]chat.ScoredMessageChunk, error) {
	result, err := c.core.Search(ctx, rag.SearchRequest{Query: query, TopK: limit})
	if err != nil {
		return nil, err
	}
	candidates := make([]chat.ScoredMessageChunk, 0, len(result.Hits))
	for _, hit := range result.Hits {
		candidates = append(candidates, chat.ScoredMessageChunk{
			StorageID: hit.Chunk.DocumentID,
			Position:  hit.Chunk.Position,
			Snippet:   hit.Chunk.Content,
			Score:     hit.RRFScore,
		})
	}
	return candidates, nil
}
