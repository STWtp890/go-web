package rag

import (
	"context"
	"testing"
)

func TestIngestAndHybridSearch(t *testing.T) {
	ctx := context.Background()
	service, err := NewService(ctx)
	if err != nil {
		t.Fatal(err)
	}

	documents := []Document{
		{ID: "eino", Title: "Eino", Content: "Eino 是 Go 语言的 AI 应用编排框架，可以把组件组织成 workflow。"},
		{ID: "qdrant", Title: "Hybrid Search", Content: "Qdrant 可以同时召回 dense vector 与 sparse keyword 结果，并使用 RRF 完成 hybrid search 融合。"},
		{ID: "grpc", Title: "gRPC", Content: "gRPC 使用 protobuf 定义跨语言的远程过程调用接口。"},
	}
	for _, document := range documents {
		result, ingestErr := service.Ingest(ctx, IngestRequest{Document: document})
		if ingestErr != nil {
			t.Fatalf("ingest %s: %v", document.ID, ingestErr)
		}
		if result.ChunkCount != 1 {
			t.Fatalf("ingest %s returned %d chunks", document.ID, result.ChunkCount)
		}
	}

	result, err := service.Search(ctx, SearchRequest{Query: "dense sparse RRF hybrid search", TopK: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Hits) != 2 {
		t.Fatalf("got %d hits, want 2", len(result.Hits))
	}
	if result.Hits[0].Chunk.DocumentID != "qdrant" {
		t.Fatalf("top document = %s, want qdrant", result.Hits[0].Chunk.DocumentID)
	}
	if result.Hits[0].DenseRank == 0 || result.Hits[0].SparseRank == 0 {
		t.Fatalf("top hit did not participate in both recall paths: %+v", result.Hits[0])
	}
}

func TestReingestReplacesDocument(t *testing.T) {
	ctx := context.Background()
	service, err := NewService(ctx)
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.Ingest(ctx, IngestRequest{Document: Document{ID: "doc", Content: "old unique phrase"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Ingest(ctx, IngestRequest{Document: Document{ID: "doc", Content: "new replacement content"}})
	if err != nil {
		t.Fatal(err)
	}

	result, err := service.Search(ctx, SearchRequest{Query: "old unique phrase", TopK: 3})
	if err != nil {
		t.Fatal(err)
	}
	for _, hit := range result.Hits {
		if hit.Chunk.Content == "old unique phrase" {
			t.Fatal("old chunk remained after replacement")
		}
	}
}

func TestValidation(t *testing.T) {
	ctx := context.Background()
	service, err := NewService(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if _, err = service.Ingest(ctx, IngestRequest{}); err == nil {
		t.Fatal("empty document should fail")
	}
	if _, err = service.Search(ctx, SearchRequest{}); err == nil {
		t.Fatal("empty query should fail")
	}
}
