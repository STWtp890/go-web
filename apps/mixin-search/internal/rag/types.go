package rag

import (
	"errors"

	documentpipeline "mixin-search/document_pipeline"
)

var ErrInvalidInput = errors.New("invalid input")

const (
	DocumentFormatMarkdown = documentpipeline.FormatMarkdown
	DocumentFormatDOC      = documentpipeline.FormatDOC
	DocumentFormatDOCX     = documentpipeline.FormatDOCX
)

// Document is the input accepted by the minimal ingestion workflow.
type Document struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Content string `json:"content"`
	Format  string `json:"format,omitempty"`
	Source  string `json:"source,omitempty"`
}

type DocumentBlock = documentpipeline.Block
type FileIngestRequest = documentpipeline.FileRequest
type DocumentPipeline = documentpipeline.Pipeline
type DocumentPipelineRegistry = documentpipeline.Registry

func NewDocumentPipelineRegistry() *DocumentPipelineRegistry {
	return documentpipeline.NewRegistry()
}

// Chunk is the evidence unit stored and returned by the demo.
type Chunk struct {
	ID            string            `json:"id"`
	DocumentID    string            `json:"document_id"`
	VersionID     string            `json:"version_id,omitempty"`
	OwnerSpaceID  string            `json:"owner_space_id,omitempty"`
	Title         string            `json:"title"`
	Content       string            `json:"content"`
	Position      int               `json:"position"`
	Format        string            `json:"format,omitempty"`
	Source        string            `json:"source,omitempty"`
	Section       string            `json:"section,omitempty"`
	ContentSHA256 string            `json:"content_sha256,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

// IngestRequest controls the deliberately small, rune-based chunker.
type IngestRequest struct {
	Document  Document        `json:"document"`
	Blocks    []DocumentBlock `json:"blocks,omitempty"`
	ChunkSize int             `json:"chunk_size"`
	Overlap   int             `json:"overlap"`
}

// IngestDocumentRequest accepts uploaded document bytes for RPC/MCP adapters.
type IngestDocumentRequest struct {
	DocumentID string `json:"document_id,omitempty"`
	Filename   string `json:"filename"`
	Title      string `json:"title,omitempty"`
	Content    []byte `json:"content"`
	ChunkSize  int    `json:"chunk_size,omitempty"`
	Overlap    int    `json:"overlap,omitempty"`
}

// IngestResult reports what the Eino ingestion workflow stored.
type IngestResult struct {
	DocumentID string `json:"document_id"`
	ChunkCount int    `json:"chunk_count"`
}

// SearchRequest asks the Eino search workflow for hybrid results.
type SearchRequest struct {
	Query string `json:"query"`
	TopK  int    `json:"top_k"`
}

// SearchHit exposes both recall paths and the final RRF score.
type SearchHit struct {
	Chunk       Chunk   `json:"chunk"`
	RRFScore    float64 `json:"rrf_score"`
	DenseRank   int     `json:"dense_rank,omitempty"`
	SparseRank  int     `json:"sparse_rank,omitempty"`
	DenseScore  float64 `json:"dense_score,omitempty"`
	SparseScore float64 `json:"sparse_score,omitempty"`
}

// SearchResult is transport-neutral and can later be mapped to gRPC or MCP.
type SearchResult struct {
	Query string      `json:"query"`
	Hits  []SearchHit `json:"hits"`
}
