package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"mixin-search/internal/rag"
)

func main() {
	query := flag.String("query", "dense sparse RRF hybrid search", "query text")
	topK := flag.Int("top-k", 3, "number of results")
	backend := flag.String("store", "memory", "vector store: memory, qdrant, or pgvector")
	qdrantHost := flag.String("qdrant-host", "localhost", "Qdrant gRPC host")
	qdrantPort := flag.Int("qdrant-port", 6334, "Qdrant gRPC port")
	qdrantCollection := flag.String("qdrant-collection", "rag_chunks", "Qdrant collection")
	pgDSN := flag.String("pg-dsn", defaultPGDSN(), "PostgreSQL connection string")
	filePath := flag.String("file", "", "path to a .md, .markdown, .doc, or .docx document")
	documentID := flag.String("document-id", "", "optional document ID for -file")
	documentTitle := flag.String("title", "", "optional document title for -file")
	chunkSize := flag.Int("chunk-size", 180, "chunk size in runes")
	overlap := flag.Int("overlap", 20, "overlap in runes")
	flag.Parse()

	ctx := context.Background()
	store, err := openStore(ctx, *backend, *qdrantHost, *qdrantPort, *qdrantCollection, *pgDSN)
	if err != nil {
		log.Fatal(err)
	}
	service, err := rag.NewServiceWithStore(ctx, store)
	if err != nil {
		_ = store.Close()
		log.Fatal(err)
	}
	defer func() {
		if closeErr := service.Close(); closeErr != nil {
			log.Printf("close store: %v", closeErr)
		}
	}()

	if *filePath != "" {
		result, ingestErr := service.IngestFile(ctx, rag.FileIngestRequest{
			Path:       *filePath,
			DocumentID: *documentID,
			Title:      *documentTitle,
			ChunkSize:  *chunkSize,
			Overlap:    *overlap,
		})
		if ingestErr != nil {
			log.Fatal(ingestErr)
		}
		fmt.Fprintf(os.Stderr, "indexed document=%s chunks=%d\n", result.DocumentID, result.ChunkCount)
	} else {
		documents := []rag.Document{
			{ID: "eino", Title: "Eino", Content: "Eino 是 Go 语言的 AI 应用编排框架，可以把 Loader、Transformer、Embedding、Retriever 等组件组织成 workflow。"},
			{ID: "hybrid", Title: "混合检索", Content: "混合检索同时运行 dense vector 语义召回和 sparse keyword 词项召回，再使用 RRF 按排名融合结果。"},
			{ID: "transport", Title: "服务接口", Content: "业务 Service 可以复用在 gRPC 与 MCP 适配器后面，让两种协议共享同一套检索逻辑。"},
		}
		for _, document := range documents {
			result, ingestErr := service.Ingest(ctx, rag.IngestRequest{Document: document})
			if ingestErr != nil {
				log.Fatal(ingestErr)
			}
			fmt.Fprintf(os.Stderr, "indexed document=%s chunks=%d\n", result.DocumentID, result.ChunkCount)
		}
	}

	result, err := service.Search(ctx, rag.SearchRequest{Query: *query, TopK: *topK})
	if err != nil {
		log.Fatal(err)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		log.Fatal(err)
	}
}

func openStore(
	ctx context.Context,
	backend string,
	qdrantHost string,
	qdrantPort int,
	qdrantCollection string,
	pgDSN string,
) (rag.VectorStore, error) {
	switch strings.ToLower(backend) {
	case "memory":
		return rag.NewMemoryStore(), nil
	case "qdrant":
		return rag.NewQdrantStore(ctx, rag.QdrantConfig{
			Host:       qdrantHost,
			Port:       qdrantPort,
			APIKey:     os.Getenv("QDRANT_API_KEY"),
			Collection: qdrantCollection,
			Dimensions: 64,
		})
	case "pgvector":
		return rag.NewPGVectorStore(ctx, rag.PGVectorConfig{
			DSN:        pgDSN,
			Dimensions: 64,
		})
	default:
		return nil, fmt.Errorf("unknown vector store %q", backend)
	}
}

func defaultPGDSN() string {
	if value := os.Getenv("PGVECTOR_DSN"); value != "" {
		return value
	}
	return "postgres://rag:rag@localhost:5432/rag?sslmode=disable"
}
