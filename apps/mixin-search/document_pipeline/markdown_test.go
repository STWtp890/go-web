package documentpipeline

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMarkdownPipelinePreservesSectionsAndCode(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "guide.md")
	content := `---
title: ignored front matter
---
# RAG Guide

Use **dense** and [sparse](https://example.com) retrieval.

## Code

` + "```go\n" + `service.IngestFile(ctx, request)
` + "```\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	blocks, err := (MarkdownPipeline{}).Load(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 4 {
		t.Fatalf("got %d blocks, want 4: %+v", len(blocks), blocks)
	}
	if blocks[1].Text != "Use dense and sparse retrieval." {
		t.Fatalf("Markdown inline markup was not normalized: %+v", blocks[1])
	}
	if blocks[3].Kind != "code" || blocks[3].Section != "RAG Guide / Code" || !strings.Contains(blocks[3].Text, "IngestFile") {
		t.Fatalf("heading context or code block was lost: %+v", blocks[3])
	}
	if strings.Contains(joinBlocks(blocks), "ignored front matter") {
		t.Fatal("front matter was not removed")
	}
}
