package rag

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestQdrantStoreIntegration(t *testing.T) {
	if os.Getenv("QDRANT_INTEGRATION") != "1" {
		t.Skip("set QDRANT_INTEGRATION=1 to run")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := NewQdrantStore(ctx, QdrantConfig{
		Host:       "localhost",
		Port:       6334,
		Collection: "rag_chunks_test",
		Dimensions: localEmbeddingDimensions,
	})
	if err != nil {
		t.Fatal(err)
	}
	runPersistentStoreContract(t, ctx, store)
}

func TestPGVectorStoreIntegration(t *testing.T) {
	if os.Getenv("PGVECTOR_INTEGRATION") != "1" {
		t.Skip("set PGVECTOR_INTEGRATION=1 to run")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dsn := os.Getenv("PGVECTOR_DSN")
	if dsn == "" {
		dsn = "postgres://rag:rag@localhost:5432/rag?sslmode=disable"
	}
	store, err := NewPGVectorStore(ctx, PGVectorConfig{
		DSN:        dsn,
		Dimensions: localEmbeddingDimensions,
	})
	if err != nil {
		t.Fatal(err)
	}
	runPersistentStoreContract(t, ctx, store)
}

func runPersistentStoreContract(t *testing.T, ctx context.Context, store VectorStore) {
	t.Helper()
	service, err := NewServiceWithStore(ctx, store)
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })

	needle := fmt.Sprintf("needle%d", time.Now().UnixNano())
	documents := []Document{
		{ID: needle, Title: "target", Content: needle + " dense sparse hybrid retrieval"},
		{ID: needle + "-other", Title: "distractor", Content: "unrelated document"},
	}
	for _, document := range documents {
		if _, err := service.Ingest(ctx, IngestRequest{Document: document}); err != nil {
			t.Fatal(err)
		}
	}
	result, err := service.Search(ctx, SearchRequest{Query: needle, TopK: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Hits) != 1 || result.Hits[0].Chunk.DocumentID != needle {
		t.Fatalf("unexpected top hit: %+v", result.Hits)
	}
}
