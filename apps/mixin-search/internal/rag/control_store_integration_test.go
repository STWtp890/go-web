package rag

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestPostgresControlStoreIntegration(t *testing.T) {
	if os.Getenv("CONTROL_STORE_INTEGRATION") != "1" {
		t.Skip("set CONTROL_STORE_INTEGRATION=1 to run PostgreSQL control-store integration tests")
	}
	dsn := os.Getenv("CONTROL_DATABASE_DSN")
	if dsn == "" {
		t.Fatal("CONTROL_DATABASE_DSN is required for control-store integration tests")
	}
	ctx := context.Background()
	namespace := fmt.Sprintf("integration-%d", time.Now().UnixNano())
	store, err := NewPostgresControlStore(ctx, PostgresControlStoreConfig{DSN: dsn, Namespace: namespace, Bootstrap: true})
	if err != nil {
		t.Fatal(err)
	}
	vectorStore := NewMemoryStore()
	service := newControlTestService(t, vectorStore, store)
	indexRequest := IndexDocumentVersionRequest{
		OperationID: "postgres-index", DocumentID: "postgres-document", VersionID: "v1",
		OwnerSpaceID: "owner", LifecycleRevision: 1, Filename: "postgres.md",
		Content: []byte("postgresrestartneedle"), SourceURI: "document://postgres/v1",
		Metadata: map[string]string{"nul": "before\x00after"},
	}
	indexed, err := service.IndexDocumentVersion(ctx, indexRequest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ActivateDocumentVersion(ctx, ActivateDocumentVersionRequest{
		OperationID: "postgres-activate", DocumentID: "postgres-document", VersionID: "v1",
		ActivationRevision: 1, LifecycleRevision: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdateDocumentAccess(ctx, UpdateDocumentAccessRequest{
		OperationID: "postgres-access", DocumentID: "postgres-document", AccessRevision: 1,
		LifecycleRevision: 1, AuthenticatedPublic: true,
	}); err != nil {
		t.Fatal(err)
	}
	store.Close()

	reopened, err := NewPostgresControlStore(ctx, PostgresControlStoreConfig{DSN: dsn, Namespace: namespace})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = reopened.pool.Exec(context.Background(), "DELETE FROM mixin_search_control.control_states WHERE namespace = $1", namespace)
		reopened.Close()
	}()
	restarted := newControlTestService(t, vectorStore, reopened)
	result, err := restarted.SearchDocuments(ctx, SearchDocumentsRequest{Query: "postgresrestartneedle", TopK: 1})
	if err != nil || len(result.Hits) != 1 || result.Hits[0].Chunk.Metadata["nul"] != "before\x00after" {
		t.Fatalf("restart search hits=%d error=%v", len(result.Hits), err)
	}
	replayed, err := restarted.IndexDocumentVersion(ctx, indexRequest)
	if err != nil || replayed != indexed {
		t.Fatalf("restart replay=%+v error=%v, want %+v", replayed, err, indexed)
	}

	left, err := reopened.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	right, err := reopened.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Save(ctx, left.Generation, left); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Save(ctx, right.Generation, right); !errors.Is(err, ErrControlStoreConflict) {
		t.Fatalf("PostgreSQL stale save error = %v, want ErrControlStoreConflict", err)
	}

	deleted, err := restarted.DeleteDocument(ctx, DeleteDocumentRequest{
		OperationID: "postgres-delete", DocumentID: "postgres-document", LifecycleRevision: 2,
	})
	if err != nil || !deleted.Tombstoned {
		t.Fatalf("PostgreSQL delete=%+v error=%v", deleted, err)
	}
	restartedAgain := newControlTestService(t, vectorStore, reopened)
	if _, err := restartedAgain.IndexDocumentVersion(ctx, IndexDocumentVersionRequest{
		OperationID: "postgres-late", DocumentID: "postgres-document", VersionID: "late",
		OwnerSpaceID: "owner", LifecycleRevision: 1, Filename: "late.md", Content: []byte("late"),
	}); !errors.Is(err, ErrStaleLifecycle) {
		t.Fatalf("late event after PostgreSQL restart = %v, want ErrStaleLifecycle", err)
	}

	isolated, err := NewPostgresControlStore(ctx, PostgresControlStoreConfig{DSN: dsn, Namespace: namespace + "-isolated", Bootstrap: true})
	if err != nil {
		t.Fatal(err)
	}
	isolatedState, err := isolated.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if isolatedState.Generation != 0 || len(isolatedState.Manifests) != 0 {
		t.Fatalf("isolated namespace state = %+v", isolatedState)
	}
	_, _ = isolated.pool.Exec(ctx, "DELETE FROM mixin_search_control.control_states WHERE namespace = $1", namespace+"-isolated")
	isolated.Close()
	if _, err := NewPostgresControlStore(ctx, PostgresControlStoreConfig{
		DSN: dsn, Namespace: namespace + "-missing",
	}); !errors.Is(err, ErrControlStoreUninitialized) {
		t.Fatalf("missing namespace error = %v, want ErrControlStoreUninitialized", err)
	}
}
