package documentpipeline

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDOCPipelineUsesLegacyParser(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "legacy.doc")
	if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	pipeline := DOCPipeline{
		parse: func(io.Reader) (io.Reader, error) {
			return strings.NewReader("First line\rSecond line\r\rThird paragraph"), nil
		},
	}
	blocks, err := pipeline.Load(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 2 || blocks[0].Text != "First line Second line" || blocks[1].Text != "Third paragraph" {
		t.Fatalf("unexpected DOC blocks: %+v", blocks)
	}
	uploadedBlocks, err := pipeline.LoadBytes(context.Background(), "legacy.doc", []byte("fixture"))
	if err != nil {
		t.Fatal(err)
	}
	if len(uploadedBlocks) != len(blocks) || uploadedBlocks[0] != blocks[0] {
		t.Fatalf("uploaded DOC produced different blocks: %+v", uploadedBlocks)
	}
}
