package grpcadapter

import (
	"context"
	"net"
	"testing"
	"time"

	"mixin-search/internal/rag"
	mixinsearchv1 "packages/gen/mixin-search/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func TestGRPCDocumentVersionLifecycle(t *testing.T) {
	client := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	indexed, err := client.IndexDocumentVersion(ctx, &mixinsearchv1.IndexDocumentVersionRequest{
		OperationId:  "index-v1",
		DocumentId:   "grpc-guide",
		VersionId:    "v1",
		OwnerSpaceId: "space-private",
		Filename:     "guide.md",
		Content:      []byte("# RPC Guide\n\ngrpcuniqueword uses dense and sparse retrieval."),
		ChunkSize:    200,
		Overlap:      20,
		SourceUri:    "go-web://documents/grpc-guide/versions/v1",
		Metadata:     map[string]string{"author_id": "user-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if indexed.GetState().GetStatus() != mixinsearchv1.DocumentIndexStatus_DOCUMENT_INDEX_STATUS_INDEXED {
		t.Fatalf("unexpected index state: %+v", indexed.GetState())
	}

	beforeActivation, err := client.SearchDocuments(ctx, &mixinsearchv1.SearchDocumentsRequest{
		Query:           "grpcuniqueword",
		AllowedSpaceIds: []string{"space-private"},
		TopK:            1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(beforeActivation.GetHits()) != 0 {
		t.Fatalf("inactive version leaked into search: %+v", beforeActivation.GetHits())
	}

	activated, err := client.ActivateDocumentVersion(ctx, &mixinsearchv1.ActivateDocumentVersionRequest{
		OperationId:        "activate-v1",
		DocumentId:         "grpc-guide",
		VersionId:          "v1",
		ActivationRevision: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if activated.GetState().GetStatus() != mixinsearchv1.DocumentIndexStatus_DOCUMENT_INDEX_STATUS_ACTIVE {
		t.Fatalf("unexpected activation state: %+v", activated.GetState())
	}

	searched, err := client.SearchDocuments(ctx, &mixinsearchv1.SearchDocumentsRequest{
		Query:           "grpcuniqueword",
		AllowedSpaceIds: []string{"space-private"},
		TopK:            1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(searched.GetHits()) != 1 {
		t.Fatalf("got %d hits, want 1", len(searched.GetHits()))
	}
	chunk := searched.GetHits()[0].GetChunk()
	if chunk.GetDocumentId() != "grpc-guide" || chunk.GetVersionId() != "v1" ||
		chunk.GetOwnerSpaceId() != "space-private" || chunk.GetFormat() != rag.DocumentFormatMarkdown ||
		chunk.GetSection() != "RPC Guide" || chunk.GetMetadata()["author_id"] != "user-1" {
		t.Fatalf("unexpected search chunk: %+v", chunk)
	}

	unauthorized, err := client.SearchDocuments(ctx, &mixinsearchv1.SearchDocumentsRequest{
		Query:           "grpcuniqueword",
		AllowedSpaceIds: []string{"another-space"},
		TopK:            1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(unauthorized.GetHits()) != 0 {
		t.Fatalf("space filter leaked hits: %+v", unauthorized.GetHits())
	}

	state, err := client.GetDocumentVersionState(ctx, &mixinsearchv1.GetDocumentVersionStateRequest{
		DocumentId: "grpc-guide",
		VersionId:  "v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !state.GetExists() || state.GetState().GetActivationRevision() != 1 {
		t.Fatalf("unexpected version state: %+v", state)
	}

	deleted, err := client.DeleteDocumentVersion(ctx, &mixinsearchv1.DeleteDocumentVersionRequest{
		OperationId: "delete-v1",
		DocumentId:  "grpc-guide",
		VersionId:   "v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !deleted.GetDeleted() || !deleted.GetActiveVersionRemoved() {
		t.Fatalf("unexpected delete response: %+v", deleted)
	}
}

func TestGRPCRejectsStaleActivation(t *testing.T) {
	client := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for _, version := range []string{"v1", "v2"} {
		_, err := client.IndexDocumentVersion(ctx, &mixinsearchv1.IndexDocumentVersionRequest{
			OperationId:  "index-" + version,
			DocumentId:   "doc",
			VersionId:    version,
			OwnerSpaceId: "space",
			Filename:     "guide.md",
			Content:      []byte("content " + version),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err := client.ActivateDocumentVersion(ctx, &mixinsearchv1.ActivateDocumentVersionRequest{
		OperationId: "activate-v1", DocumentId: "doc", VersionId: "v1", ActivationRevision: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	activateV2 := &mixinsearchv1.ActivateDocumentVersionRequest{
		OperationId:               "activate-v2",
		DocumentId:                "doc",
		VersionId:                 "v2",
		ActivationRevision:        2,
		ExpectedPreviousVersionId: "v1",
	}
	if _, err = client.ActivateDocumentVersion(ctx, activateV2); err != nil {
		t.Fatal(err)
	}
	if _, err = client.ActivateDocumentVersion(ctx, activateV2); err != nil {
		t.Fatalf("idempotent activation retry failed: %v", err)
	}
	v1State, err := client.GetDocumentVersionState(ctx, &mixinsearchv1.GetDocumentVersionStateRequest{
		DocumentId: "doc", VersionId: "v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if v1State.GetState().GetStatus() != mixinsearchv1.DocumentIndexStatus_DOCUMENT_INDEX_STATUS_INDEXED ||
		v1State.GetState().GetActivationRevision() != 0 {
		t.Fatalf("previous version remained active: %+v", v1State.GetState())
	}
	_, err = client.IndexDocumentVersion(ctx, &mixinsearchv1.IndexDocumentVersionRequest{
		OperationId:  "replace-v2",
		DocumentId:   "doc",
		VersionId:    "v2",
		OwnerSpaceId: "space",
		Filename:     "guide.md",
		Content:      []byte("different content"),
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("immutable version status = %s, want %s; error=%v", status.Code(err), codes.FailedPrecondition, err)
	}
	_, err = client.ActivateDocumentVersion(ctx, &mixinsearchv1.ActivateDocumentVersionRequest{
		OperationId: "stale-v1", DocumentId: "doc", VersionId: "v1", ActivationRevision: 1,
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("status code = %s, want %s; error=%v", status.Code(err), codes.FailedPrecondition, err)
	}
}

func TestGRPCAccessFencingAndORAuthorization(t *testing.T) {
	client := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := client.IndexDocumentVersion(ctx, &mixinsearchv1.IndexDocumentVersionRequest{
		OperationId:  "index-access-v1",
		DocumentId:   "access-doc",
		VersionId:    "v1",
		OwnerSpaceId: "owner-space",
		Filename:     "access.md",
		Content:      []byte("accessuniqueword"),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.ActivateDocumentVersion(ctx, &mixinsearchv1.ActivateDocumentVersionRequest{
		OperationId: "activate-access-v1", DocumentId: "access-doc", VersionId: "v1", ActivationRevision: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	assertSearchHitCount(t, ctx, client, &mixinsearchv1.SearchDocumentsRequest{
		Query: "accessuniqueword", TopK: 1,
	}, 0)

	grant := &mixinsearchv1.UpdateDocumentAccessRequest{
		OperationId:     "access-grant-1",
		DocumentId:      "access-doc",
		AccessRevision:  1,
		GrantedSpaceIds: []string{" shared-space ", "shared-space"},
	}
	access, err := client.UpdateDocumentAccess(ctx, grant)
	if err != nil {
		t.Fatal(err)
	}
	if got := access.GetState().GetGrantedSpaceIds(); len(got) != 1 || got[0] != "shared-space" {
		t.Fatalf("granted space snapshot was not normalized: %v", got)
	}
	if _, err = client.UpdateDocumentAccess(ctx, grant); err != nil {
		t.Fatalf("idempotent access retry failed: %v", err)
	}
	assertSearchHitCount(t, ctx, client, &mixinsearchv1.SearchDocumentsRequest{
		Query: "accessuniqueword", AllowedSpaceIds: []string{"shared-space"}, TopK: 1,
	}, 1)
	assertSearchHitCount(t, ctx, client, &mixinsearchv1.SearchDocumentsRequest{
		Query: "accessuniqueword", AllowedDocumentIds: []string{"access-doc"}, TopK: 1,
	}, 1)

	public := &mixinsearchv1.UpdateDocumentAccessRequest{
		OperationId: "access-public-2", DocumentId: "access-doc", AccessRevision: 2, AuthenticatedPublic: true,
	}
	if _, err = client.UpdateDocumentAccess(ctx, public); err != nil {
		t.Fatal(err)
	}
	if _, err = client.UpdateDocumentAccess(ctx, public); err != nil {
		t.Fatalf("idempotent public retry failed: %v", err)
	}
	assertSearchHitCount(t, ctx, client, &mixinsearchv1.SearchDocumentsRequest{
		Query: "accessuniqueword", TopK: 1,
	}, 1)

	_, err = client.UpdateDocumentAccess(ctx, &mixinsearchv1.UpdateDocumentAccessRequest{
		OperationId: "access-conflict-2", DocumentId: "access-doc", AccessRevision: 2,
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("same revision conflict status = %s, want %s; error=%v", status.Code(err), codes.FailedPrecondition, err)
	}
	_, err = client.UpdateDocumentAccess(ctx, &mixinsearchv1.UpdateDocumentAccessRequest{
		OperationId: "access-stale-1", DocumentId: "access-doc", AccessRevision: 1,
		GrantedSpaceIds: []string{"late-space"},
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("stale access status = %s, want %s; error=%v", status.Code(err), codes.FailedPrecondition, err)
	}
}

func TestGRPCLifecycleTombstoneAndRepublish(t *testing.T) {
	client := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := client.IndexDocumentVersion(ctx, &mixinsearchv1.IndexDocumentVersionRequest{
		OperationId: "index-life-v1", DocumentId: "life-doc", VersionId: "v1",
		OwnerSpaceId: "owner-space", Filename: "life.md", Content: []byte("lifecycleuniqueword"),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.ActivateDocumentVersion(ctx, &mixinsearchv1.ActivateDocumentVersionRequest{
		OperationId: "activate-life-v1", DocumentId: "life-doc", VersionId: "v1", ActivationRevision: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	deleted, err := client.DeleteDocument(ctx, &mixinsearchv1.DeleteDocumentRequest{
		OperationId: "delete-life-1", DocumentId: "life-doc", LifecycleRevision: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !deleted.GetTombstoned() || deleted.GetLifecycleRevision() != 1 {
		t.Fatalf("unexpected tombstone response: %+v", deleted)
	}
	if _, err = client.DeleteDocument(ctx, &mixinsearchv1.DeleteDocumentRequest{
		OperationId: "delete-life-1-retry", DocumentId: "life-doc", LifecycleRevision: 1,
	}); err != nil {
		t.Fatalf("idempotent delete retry failed: %v", err)
	}
	assertSearchHitCount(t, ctx, client, &mixinsearchv1.SearchDocumentsRequest{
		Query: "lifecycleuniqueword", AllowedDocumentIds: []string{"life-doc"}, TopK: 1,
	}, 0)

	_, err = client.IndexDocumentVersion(ctx, &mixinsearchv1.IndexDocumentVersionRequest{
		OperationId: "late-index-life-v1", DocumentId: "life-doc", VersionId: "v1",
		OwnerSpaceId: "owner-space", Filename: "life.md", Content: []byte("lifecycleuniqueword"),
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("late index status = %s, want %s; error=%v", status.Code(err), codes.FailedPrecondition, err)
	}

	_, err = client.IndexDocumentVersion(ctx, &mixinsearchv1.IndexDocumentVersionRequest{
		OperationId: "index-life-v2", DocumentId: "life-doc", VersionId: "v2",
		OwnerSpaceId: "owner-space", Filename: "life.md", Content: []byte("republished lifecycleuniqueword"),
		LifecycleRevision: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.ActivateDocumentVersion(ctx, &mixinsearchv1.ActivateDocumentVersionRequest{
		OperationId: "activate-life-v2", DocumentId: "life-doc", VersionId: "v2",
		ActivationRevision: 2, LifecycleRevision: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertSearchHitCount(t, ctx, client, &mixinsearchv1.SearchDocumentsRequest{
		Query: "lifecycleuniqueword", AllowedSpaceIds: []string{"owner-space"}, TopK: 1,
	}, 1)

	retriedDelete, err := client.DeleteDocument(ctx, &mixinsearchv1.DeleteDocumentRequest{
		OperationId: "late-delete-life-1", DocumentId: "life-doc", LifecycleRevision: 1,
	})
	if err != nil || !retriedDelete.GetTombstoned() {
		t.Fatalf("old delete natural-key retry was not idempotent: response=%+v error=%v", retriedDelete, err)
	}
	assertSearchHitCount(t, ctx, client, &mixinsearchv1.SearchDocumentsRequest{
		Query: "lifecycleuniqueword", AllowedSpaceIds: []string{"owner-space"}, TopK: 1,
	}, 1)
	if _, err = client.DeleteDocument(ctx, &mixinsearchv1.DeleteDocumentRequest{
		OperationId: "delete-life-3", DocumentId: "life-doc", LifecycleRevision: 3,
	}); err != nil {
		t.Fatal(err)
	}
	_, err = client.DeleteDocument(ctx, &mixinsearchv1.DeleteDocumentRequest{
		OperationId: "unknown-stale-delete-2", DocumentId: "life-doc", LifecycleRevision: 2,
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("stale lifecycle status = %s, want %s; error=%v", status.Code(err), codes.FailedPrecondition, err)
	}

	if _, err = client.DeleteDocument(ctx, &mixinsearchv1.DeleteDocumentRequest{
		OperationId: "delete-before-index", DocumentId: "future-doc", LifecycleRevision: 5,
	}); err != nil {
		t.Fatal(err)
	}
	_, err = client.IndexDocumentVersion(ctx, &mixinsearchv1.IndexDocumentVersionRequest{
		OperationId: "stale-future-index", DocumentId: "future-doc", VersionId: "v1",
		OwnerSpaceId: "owner-space", Filename: "future.md", Content: []byte("future"),
		LifecycleRevision: 4,
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("delete-before-index fence status = %s, want %s; error=%v", status.Code(err), codes.FailedPrecondition, err)
	}
}

func assertSearchHitCount(
	t *testing.T,
	ctx context.Context,
	client mixinsearchv1.RAGServiceClient,
	request *mixinsearchv1.SearchDocumentsRequest,
	want int,
) {
	t.Helper()
	response, err := client.SearchDocuments(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(response.GetHits()); got != want {
		t.Fatalf("search hit count = %d, want %d; response=%+v", got, want, response)
	}
}

func TestGRPCValidationError(t *testing.T) {
	client := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := client.IndexDocumentVersion(ctx, &mixinsearchv1.IndexDocumentVersionRequest{
		OperationId:  "invalid-format",
		DocumentId:   "doc",
		VersionId:    "v1",
		OwnerSpaceId: "space",
		Filename:     "guide.pdf",
		Content:      []byte("not supported"),
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("status code = %s, want %s; error=%v", status.Code(err), codes.InvalidArgument, err)
	}

	_, err = client.SearchDocuments(ctx, &mixinsearchv1.SearchDocumentsRequest{Query: "query", TopK: 101})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("invalid top_k status = %s, want %s; error=%v", status.Code(err), codes.InvalidArgument, err)
	}
}

func newTestClient(t *testing.T) mixinsearchv1.RAGServiceClient {
	t.Helper()
	core, err := rag.NewService(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	contractService, err := rag.NewDocumentIndexService(core)
	if err != nil {
		_ = core.Close()
		t.Fatal(err)
	}
	handler, err := NewServer(contractService)
	if err != nil {
		_ = core.Close()
		t.Fatal(err)
	}
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	mixinsearchv1.RegisterRAGServiceServer(server, handler)
	go func() {
		_ = server.Serve(listener)
	}()

	connection, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
	)
	if err != nil {
		server.Stop()
		_ = core.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = connection.Close()
		server.Stop()
		_ = listener.Close()
		_ = core.Close()
	})
	return mixinsearchv1.NewRAGServiceClient(connection)
}
