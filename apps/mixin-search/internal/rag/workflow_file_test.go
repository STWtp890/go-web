package rag

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMarkdownFileUsesSharedEinoWorkflow(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "search.md")
	if err := os.WriteFile(path, []byte("# Search\n\nuniquepipelineword uses dense and sparse retrieval."), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	service, err := NewService(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	if _, err := service.IngestFile(ctx, FileIngestRequest{Path: path}); err != nil {
		t.Fatal(err)
	}
	result, err := service.Search(ctx, SearchRequest{Query: "uniquepipelineword", TopK: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Hits) != 1 || result.Hits[0].Chunk.Format != DocumentFormatMarkdown {
		t.Fatalf("unexpected file search result: %+v", result.Hits)
	}
	if result.Hits[0].Chunk.Section != "Search" || !strings.Contains(result.Hits[0].Chunk.Content, "uniquepipelineword") {
		t.Fatalf("structured chunk metadata was lost: %+v", result.Hits[0].Chunk)
	}
}
