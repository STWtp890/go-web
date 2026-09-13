// Package documentpipeline converts format-specific files into common blocks
// consumed by the RAG embedding workflow.
package documentpipeline

import "context"

const (
	FormatMarkdown = "markdown"
	FormatDOC      = "doc"
	FormatDOCX     = "docx"
)

// Block is the normalized intermediate representation emitted by every file
// format pipeline.
type Block struct {
	Kind    string `json:"kind"`
	Section string `json:"section,omitempty"`
	Text    string `json:"text"`
}

// FileRequest identifies a local source document and its chunking options.
type FileRequest struct {
	Path       string `json:"path"`
	DocumentID string `json:"document_id,omitempty"`
	Title      string `json:"title,omitempty"`
	ChunkSize  int    `json:"chunk_size,omitempty"`
	Overlap    int    `json:"overlap,omitempty"`
}

// BytesRequest carries an uploaded document without exposing a server-local
// filesystem path to remote transports.
type BytesRequest struct {
	Filename   string
	DocumentID string
	Title      string
	Content    []byte
	ChunkSize  int
	Overlap    int
}

// LoadedDocument is format-neutral and ready for the shared split/embed/store
// workflow.
type LoadedDocument struct {
	ID        string
	Title     string
	Content   string
	Format    string
	Source    string
	Blocks    []Block
	ChunkSize int
	Overlap   int
}

// Pipeline parses one source format into common semantic blocks.
type Pipeline interface {
	Format() string
	Extensions() []string
	Load(ctx context.Context, path string) ([]Block, error)
}

// BytesPipeline is implemented by pipelines that can parse uploaded content
// directly in memory.
type BytesPipeline interface {
	Pipeline
	LoadBytes(ctx context.Context, filename string, content []byte) ([]Block, error)
}
