package rag

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	documentpipeline "mixin-search/document_pipeline"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/compose"
)

type chunkBatch struct {
	DocumentID string
	Chunks     []Chunk
	Texts      []string
}

type vectorizeInput struct {
	Batch chunkBatch
	Dense [][]float64
}

type indexBatch struct {
	DocumentID string
	Chunks     []IndexedChunk
}

type preparedQuery struct {
	Query      string
	TopK       int
	CandidateK int
	Tokens     []string
	Texts      []string
}

type denseRecallInput struct {
	CandidateK int
	Vectors    [][]float64
}

type fusionInput struct {
	Query      string
	TopK       int
	DenseHits  []ScoredChunk
	SparseHits []ScoredChunk
}

// Service owns the compiled Eino workflows and the small business API.
type Service struct {
	store             VectorStore
	documentPipelines *DocumentPipelineRegistry
	ingestWorkflow    compose.Runnable[IngestRequest, IngestResult]
	searchWorkflow    compose.Runnable[SearchRequest, SearchResult]
}

func NewService(ctx context.Context) (*Service, error) {
	return NewServiceWithComponents(ctx, NewMemoryStore(), LocalEmbedder{})
}

// NewServiceWithStore keeps the Eino and business workflows unchanged while
// selecting a real vector backend.
func NewServiceWithStore(ctx context.Context, store VectorStore) (*Service, error) {
	return NewServiceWithComponents(ctx, store, LocalEmbedder{})
}

// NewServiceWithComponents allows both storage and embedding implementations
// to be replaced without changing the workflows.
func NewServiceWithComponents(
	ctx context.Context,
	store VectorStore,
	embedder embedding.Embedder,
) (*Service, error) {
	if store == nil {
		return nil, errors.New("vector store is required")
	}
	if embedder == nil {
		return nil, errors.New("embedder is required")
	}

	ingestWorkflow, err := buildIngestWorkflow(ctx, store, embedder)
	if err != nil {
		return nil, fmt.Errorf("compile ingest workflow: %w", err)
	}
	searchWorkflow, err := buildSearchWorkflow(ctx, store, embedder)
	if err != nil {
		return nil, fmt.Errorf("compile search workflow: %w", err)
	}
	return &Service{
		store:             store,
		documentPipelines: NewDocumentPipelineRegistry(),
		ingestWorkflow:    ingestWorkflow,
		searchWorkflow:    searchWorkflow,
	}, nil
}

func (s *Service) Ingest(ctx context.Context, request IngestRequest) (IngestResult, error) {
	return s.ingestWorkflow.Invoke(ctx, request)
}

// IngestFile selects the Markdown, DOC, or DOCX preparation pipeline and then
// invokes the same Eino embedding and storage workflow used by Ingest.
func (s *Service) IngestFile(ctx context.Context, request FileIngestRequest) (IngestResult, error) {
	prepared, err := s.documentPipelines.Prepare(ctx, request)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return IngestResult{}, err
		}
		return IngestResult{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	return s.Ingest(ctx, IngestRequest{
		Document: Document{
			ID:      prepared.ID,
			Title:   prepared.Title,
			Content: prepared.Content,
			Format:  prepared.Format,
			Source:  prepared.Source,
		},
		Blocks:    prepared.Blocks,
		ChunkSize: prepared.ChunkSize,
		Overlap:   prepared.Overlap,
	})
}

// IngestDocument parses uploaded bytes without relying on a path in the
// server's local filesystem.
func (s *Service) IngestDocument(ctx context.Context, request IngestDocumentRequest) (IngestResult, error) {
	prepared, err := s.documentPipelines.PrepareBytes(ctx, documentpipeline.BytesRequest{
		Filename:   request.Filename,
		DocumentID: request.DocumentID,
		Title:      request.Title,
		Content:    request.Content,
		ChunkSize:  request.ChunkSize,
		Overlap:    request.Overlap,
	})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return IngestResult{}, err
		}
		return IngestResult{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	return s.Ingest(ctx, IngestRequest{
		Document: Document{
			ID:      prepared.ID,
			Title:   prepared.Title,
			Content: prepared.Content,
			Format:  prepared.Format,
			Source:  prepared.Source,
		},
		Blocks:    prepared.Blocks,
		ChunkSize: prepared.ChunkSize,
		Overlap:   prepared.Overlap,
	})
}

// RegisterDocumentPipeline allows another file format to reuse the same Eino
// chunking, embedding, and persistence stages.
func (s *Service) RegisterDocumentPipeline(pipeline DocumentPipeline) error {
	return s.documentPipelines.Register(pipeline)
}

func (s *Service) Search(ctx context.Context, request SearchRequest) (SearchResult, error) {
	return s.searchWorkflow.Invoke(ctx, request)
}

// DeleteIndexedDocument removes all chunks stored under one internal document key.
func (s *Service) DeleteIndexedDocument(ctx context.Context, documentID string) error {
	return s.store.ReplaceDocument(ctx, documentID, nil)
}

func (s *Service) Close() error {
	return s.store.Close()
}

func buildIngestWorkflow(
	ctx context.Context,
	store VectorStore,
	embedder embedding.Embedder,
) (compose.Runnable[IngestRequest, IngestResult], error) {
	workflow := compose.NewWorkflow[IngestRequest, IngestResult]()

	splitNode := compose.InvokableLambda(func(_ context.Context, request IngestRequest) (chunkBatch, error) {
		return splitRequest(request)
	})
	vectorizeNode := compose.InvokableLambda(func(_ context.Context, input vectorizeInput) (indexBatch, error) {
		if len(input.Dense) != len(input.Batch.Chunks) {
			return indexBatch{}, fmt.Errorf("embedding count %d does not match chunk count %d", len(input.Dense), len(input.Batch.Chunks))
		}
		indexed := make([]IndexedChunk, 0, len(input.Batch.Chunks))
		for i, chunk := range input.Batch.Chunks {
			tokens := tokenize(chunk.Content)
			terms := make(map[string]int, len(tokens))
			for _, token := range tokens {
				terms[token]++
			}
			indexed = append(indexed, IndexedChunk{
				Chunk:  chunk,
				Dense:  input.Dense[i],
				Terms:  terms,
				Length: len(tokens),
			})
		}
		return indexBatch{DocumentID: input.Batch.DocumentID, Chunks: indexed}, nil
	})
	storeNode := compose.InvokableLambda(func(ctx context.Context, batch indexBatch) (IngestResult, error) {
		if err := store.ReplaceDocument(ctx, batch.DocumentID, batch.Chunks); err != nil {
			return IngestResult{}, err
		}
		return IngestResult{DocumentID: batch.DocumentID, ChunkCount: len(batch.Chunks)}, nil
	})

	workflow.AddLambdaNode("split_document", splitNode).AddInput(compose.START)
	workflow.AddEmbeddingNode("dense_embedding", embedder).
		AddInput("split_document", compose.FromField("Texts"))
	workflow.AddLambdaNode("vectorize_chunks", vectorizeNode).
		AddInput("split_document", compose.ToField("Batch")).
		AddInput("dense_embedding", compose.ToField("Dense"))
	workflow.AddLambdaNode("store_chunks", storeNode).AddInput("vectorize_chunks")
	workflow.End().AddInput("store_chunks")
	return workflow.Compile(ctx)
}

func buildSearchWorkflow(
	ctx context.Context,
	store VectorStore,
	embedder embedding.Embedder,
) (compose.Runnable[SearchRequest, SearchResult], error) {
	workflow := compose.NewWorkflow[SearchRequest, SearchResult]()

	prepareNode := compose.InvokableLambda(func(_ context.Context, request SearchRequest) (preparedQuery, error) {
		query := strings.TrimSpace(request.Query)
		if query == "" {
			return preparedQuery{}, errors.New("query is required")
		}
		if request.TopK <= 0 {
			request.TopK = 3
		}
		return preparedQuery{
			Query:      query,
			TopK:       request.TopK,
			CandidateK: request.TopK * 4,
			Tokens:     tokenize(query),
			Texts:      []string{query},
		}, nil
	})
	denseNode := compose.InvokableLambda(func(ctx context.Context, input denseRecallInput) ([]ScoredChunk, error) {
		if len(input.Vectors) != 1 {
			return nil, fmt.Errorf("expected one query embedding, got %d", len(input.Vectors))
		}
		return store.DenseSearch(ctx, input.Vectors[0], input.CandidateK)
	})
	sparseNode := compose.InvokableLambda(func(ctx context.Context, query preparedQuery) ([]ScoredChunk, error) {
		return store.SparseSearch(ctx, query.Tokens, query.CandidateK)
	})
	fusionNode := compose.InvokableLambda(func(_ context.Context, input fusionInput) (SearchResult, error) {
		return fuseRRF(input), nil
	})

	workflow.AddLambdaNode("prepare_query", prepareNode).AddInput(compose.START)
	workflow.AddEmbeddingNode("dense_embedding", embedder).
		AddInput("prepare_query", compose.FromField("Texts"))
	workflow.AddLambdaNode("dense_recall", denseNode).
		AddInput("prepare_query", compose.MapFields("CandidateK", "CandidateK")).
		AddInput("dense_embedding", compose.ToField("Vectors"))
	workflow.AddLambdaNode("sparse_recall", sparseNode).AddInput("prepare_query")
	workflow.AddLambdaNode("rrf_fusion", fusionNode).
		AddInput("prepare_query",
			compose.MapFields("Query", "Query"),
			compose.MapFields("TopK", "TopK"),
		).
		AddInput("dense_recall", compose.ToField("DenseHits")).
		AddInput("sparse_recall", compose.ToField("SparseHits"))
	workflow.End().AddInput("rrf_fusion")
	return workflow.Compile(ctx)
}

func splitRequest(request IngestRequest) (chunkBatch, error) {
	document := request.Document
	document.ID = strings.TrimSpace(document.ID)
	document.Content = strings.TrimSpace(document.Content)
	if document.ID == "" {
		return chunkBatch{}, errors.New("document id is required")
	}
	if document.Content == "" {
		return chunkBatch{}, errors.New("document content is required")
	}
	if request.ChunkSize <= 0 {
		request.ChunkSize = 180
	}
	if request.Overlap < 0 || request.Overlap >= request.ChunkSize {
		return chunkBatch{}, errors.New("overlap must be >= 0 and smaller than chunk size")
	}
	if len(request.Blocks) > 0 {
		return splitStructuredDocument(document, request.Blocks, request.ChunkSize, request.Overlap)
	}
	return splitDocumentText(document, request.ChunkSize, request.Overlap, "")
}

func splitStructuredDocument(
	document Document,
	blocks []DocumentBlock,
	chunkSize int,
	overlap int,
) (chunkBatch, error) {
	type sectionGroup struct {
		section string
		texts   []string
	}
	groups := make([]sectionGroup, 0, len(blocks))
	for _, block := range blocks {
		text := strings.TrimSpace(block.Text)
		if text == "" {
			continue
		}
		section := strings.TrimSpace(block.Section)
		if len(groups) == 0 || groups[len(groups)-1].section != section {
			groups = append(groups, sectionGroup{section: section})
		}
		group := &groups[len(groups)-1]
		if block.Kind != "heading" {
			group.texts = append(group.texts, text)
		}
	}

	var chunks []Chunk
	for _, group := range groups {
		content := strings.Join(group.texts, "\n\n")
		if group.section != "" {
			if content == "" {
				content = group.section
			} else {
				content = group.section + "\n\n" + content
			}
		}
		groupDocument := document
		groupDocument.Content = content
		groupBatch, err := splitDocumentText(groupDocument, chunkSize, overlap, group.section)
		if err != nil {
			return chunkBatch{}, err
		}
		for _, chunk := range groupBatch.Chunks {
			chunk.Position = len(chunks)
			chunk.ID = fmt.Sprintf("%s#%03d", document.ID, chunk.Position)
			chunks = append(chunks, chunk)
		}
	}
	if len(chunks) == 0 {
		return chunkBatch{}, errors.New("document pipeline produced no text blocks")
	}
	texts := make([]string, len(chunks))
	for i, chunk := range chunks {
		texts[i] = chunk.Content
	}
	return chunkBatch{DocumentID: document.ID, Chunks: chunks, Texts: texts}, nil
}

func splitDocumentText(document Document, chunkSize int, overlap int, section string) (chunkBatch, error) {
	content := strings.TrimSpace(document.Content)
	if content == "" {
		return chunkBatch{}, errors.New("document content is required")
	}

	runes := []rune(content)
	chunks := make([]Chunk, 0, 1+len(runes)/chunkSize)
	for start, position := 0, 0; start < len(runes); position++ {
		end := min(start+chunkSize, len(runes))
		content := strings.TrimSpace(string(runes[start:end]))
		if content != "" {
			chunks = append(chunks, Chunk{
				ID:         fmt.Sprintf("%s#%03d", document.ID, position),
				DocumentID: document.ID,
				Title:      document.Title,
				Content:    content,
				Position:   position,
				Format:     document.Format,
				Source:     document.Source,
				Section:    section,
			})
		}
		if end == len(runes) {
			break
		}
		start = end - overlap
	}
	texts := make([]string, len(chunks))
	for i, chunk := range chunks {
		texts[i] = chunk.Content
	}
	return chunkBatch{DocumentID: document.ID, Chunks: chunks, Texts: texts}, nil
}

func fuseRRF(input fusionInput) SearchResult {
	const rankConstant = 60.0
	type aggregate struct {
		hit SearchHit
	}
	aggregates := make(map[string]*aggregate, len(input.DenseHits)+len(input.SparseHits))
	get := func(chunk Chunk) *aggregate {
		value := aggregates[chunk.ID]
		if value == nil {
			value = &aggregate{hit: SearchHit{Chunk: chunk}}
			aggregates[chunk.ID] = value
		}
		return value
	}

	for index, candidate := range input.DenseHits {
		rank := index + 1
		value := get(candidate.Chunk)
		value.hit.DenseRank = rank
		value.hit.DenseScore = candidate.Score
		value.hit.RRFScore += 1 / (rankConstant + float64(rank))
	}
	for index, candidate := range input.SparseHits {
		rank := index + 1
		value := get(candidate.Chunk)
		value.hit.SparseRank = rank
		value.hit.SparseScore = candidate.Score
		value.hit.RRFScore += 1 / (rankConstant + float64(rank))
	}

	hits := make([]SearchHit, 0, len(aggregates))
	for _, value := range aggregates {
		hits = append(hits, value.hit)
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].RRFScore == hits[j].RRFScore {
			return hits[i].Chunk.ID < hits[j].Chunk.ID
		}
		return hits[i].RRFScore > hits[j].RRFScore
	})
	if len(hits) > input.TopK {
		hits = hits[:input.TopK]
	}
	return SearchResult{Query: input.Query, Hits: hits}
}
