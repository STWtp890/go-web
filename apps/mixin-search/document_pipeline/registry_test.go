package documentpipeline

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegistryPrepareMarkdown(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "guide.md")
	if err := os.WriteFile(path, []byte("# RAG Guide\n\nDense and sparse retrieval."), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := NewRegistry().Prepare(context.Background(), FileRequest{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Format != FormatMarkdown || loaded.Title != "RAG Guide" || len(loaded.Blocks) != 2 {
		t.Fatalf("unexpected loaded document: %+v", loaded)
	}
}

func TestRegistryRejectsUnknownExtension(t *testing.T) {
	t.Parallel()

	_, err := NewRegistry().Prepare(context.Background(), FileRequest{Path: "guide.pdf"})
	if err == nil || !strings.Contains(err.Error(), "unsupported document extension") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRegistryPrepareBytes(t *testing.T) {
	t.Parallel()

	loaded, err := NewRegistry().PrepareBytes(context.Background(), BytesRequest{
		Filename:   "uploaded.md",
		DocumentID: "upload",
		Content:    []byte("# Uploaded\n\nRPC document content."),
	})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ID != "upload" || loaded.Title != "Uploaded" || loaded.Source != "uploaded.md" || loaded.Format != FormatMarkdown {
		t.Fatalf("unexpected uploaded document: %+v", loaded)
	}
}
