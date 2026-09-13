package documentpipeline

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDOCXPipelineExtractsParagraphsAndTables(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "guide.docx")
	documentXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>
  <w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>Word Guide</w:t></w:r></w:p>
  <w:p><w:r><w:t>Dense </w:t></w:r><w:r><w:t>retrieval</w:t></w:r></w:p>
  <w:tbl><w:tr><w:tc><w:p><w:r><w:t>table cell</w:t></w:r></w:p></w:tc></w:tr></w:tbl>
</w:body></w:document>`
	writeTestDOCX(t, path, documentXML)

	blocks, err := (DOCXPipeline{}).Load(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	uploadedBlocks, err := (DOCXPipeline{}).LoadBytes(context.Background(), "guide.docx", content)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 3 {
		t.Fatalf("got %d blocks, want 3: %+v", len(blocks), blocks)
	}
	if blocks[0].Kind != "heading" || blocks[1].Section != "Word Guide" || blocks[1].Text != "Dense retrieval" {
		t.Fatalf("unexpected DOCX blocks: %+v", blocks)
	}
	if blocks[2].Kind != "table" || blocks[2].Text != "table cell" {
		t.Fatalf("DOCX table text was not extracted: %+v", blocks[2])
	}
	if len(uploadedBlocks) != len(blocks) || uploadedBlocks[1] != blocks[1] {
		t.Fatalf("uploaded DOCX produced different blocks: %+v", uploadedBlocks)
	}
}

func writeTestDOCX(t *testing.T, path string, documentXML string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	entry, err := archive.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte(documentXML)); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
