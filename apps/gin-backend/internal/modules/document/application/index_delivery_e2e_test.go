package application_test

import (
	"context"
	"os"
	"testing"
	"time"

	application "gin-backend/internal/modules/document/application"
	domain "gin-backend/internal/modules/document/domain"
	"gin-backend/internal/modules/document/infrastructure/mixinsearch"
	mixinsearchv1 "packages/gen/mixin-search/v1"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestIndexDeliveryRebuildE2E(t *testing.T) {
	if os.Getenv("INDEX_DELIVERY_E2E") != "1" {
		t.Skip("INDEX_DELIVERY_E2E is not set")
	}
	address := os.Getenv("MIXIN_SEARCH_E2E_ADDRESS")
	if address == "" {
		t.Fatal("MIXIN_SEARCH_E2E_ADDRESS is not set")
	}
	db, store := openCommandRepository(t)
	ctx := context.Background()
	commands := newCommandService(t, store)

	ownerID := createCommandUser(t, db, "index-e2e-active")
	active, err := commands.Create(ctx, application.CreateCommand{
		OwnerID: ownerID, Title: "P2.3 active", Content: "p23 e2e active nebula revision one",
	})
	if err != nil {
		t.Fatalf("create active document: %v", err)
	}
	active, err = commands.Update(ctx, application.UpdateCommand{
		OwnerID: ownerID, DocumentID: active.Document.DocumentID, Title: "P2.3 active v2",
		Content: "p23 e2e active nebula revision two", AuthenticatedPublic: true,
	})
	if err != nil {
		t.Fatalf("update active document: %v", err)
	}

	trashOwnerID := createCommandUser(t, db, "index-e2e-trash")
	trashed, err := commands.Create(ctx, application.CreateCommand{
		OwnerID: trashOwnerID, Title: "P2.3 trash", Content: "p23 e2e deleted comet",
	})
	if err != nil {
		t.Fatalf("create trashed document: %v", err)
	}
	if err := commands.Trash(ctx, application.TrashCommand{OwnerID: trashOwnerID, DocumentID: trashed.Document.DocumentID}); err != nil {
		t.Fatalf("trash document: %v", err)
	}

	// 这些 transaction 事件代表旧目标已经完成。新 run 只依赖事实快照，
	// 从完全空的 mixin-search control store 与 Qdrant 重建当前状态。
	if err := db.Exec(`UPDATE document_index_delivery_events
		SET state = 'succeeded', delivered_at = clock_timestamp(), lease_owner = NULL,
		    lease_token = NULL, lease_expires_at = NULL
		WHERE state <> 'succeeded'`).Error; err != nil {
		t.Fatalf("complete historical deliveries: %v", err)
	}

	client, err := mixinsearch.New(address, 16<<20)
	if err != nil {
		t.Fatalf("new mixin-search client: %v", err)
	}
	defer client.Close()
	maintenance, err := application.NewIndexMaintenanceService(store, client)
	if err != nil {
		t.Fatalf("new maintenance service: %v", err)
	}
	runID := uuid.NewString()
	run, err := maintenance.PrepareRebuild(ctx, runID)
	if err != nil {
		t.Fatalf("prepare rebuild: %v", err)
	}
	if run.State != domain.IndexRebuildRunning {
		t.Fatalf("prepared rebuild state = %s", run.State)
	}

	worker, err := application.NewIndexWorker(store, client, "p2.3-e2e", application.IndexWorkerConfig{
		BatchSize: 100, Concurrency: 8, LeaseDuration: 2 * time.Minute,
		IndexTimeout: 30 * time.Second, ControlTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("new worker: %v", err)
	}
	if err := worker.RunOnce(ctx); err != nil {
		t.Fatalf("run rebuild deliveries: %v", err)
	}
	run, err = maintenance.RebuildStatus(ctx, runID)
	if err != nil {
		t.Fatalf("read rebuild status: %v", err)
	}
	if run.State != domain.IndexRebuildSucceeded || run.FailureCount != 0 {
		t.Fatalf("rebuild result = %#v", run)
	}

	activeState, err := client.GetDocumentVersionState(ctx, active.Document.DocumentID, active.Version.VersionID)
	if err != nil {
		t.Fatalf("get active state: %v", err)
	}
	if !activeState.Exists || activeState.Status != "DOCUMENT_INDEX_STATUS_ACTIVE" ||
		activeState.ActivationRevision != uint64(active.Document.ActivationRevision) ||
		activeState.AccessRevision != uint64(active.Document.AccessRevision) ||
		activeState.ContentSHA256 != active.Version.ContentSHA256 {
		t.Fatalf("active rebuilt state = %#v", activeState)
	}
	trashedState, err := client.GetDocumentVersionState(ctx, trashed.Document.DocumentID, trashed.Version.VersionID)
	if err != nil {
		t.Fatalf("get trashed state: %v", err)
	}
	if trashedState.Exists {
		t.Fatalf("trashed version exists after rebuild: %#v", trashedState)
	}

	connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("open search client: %v", err)
	}
	defer connection.Close()
	searchClient := mixinsearchv1.NewRAGServiceClient(connection)
	search, err := searchClient.SearchDocuments(ctx, &mixinsearchv1.SearchDocumentsRequest{
		Query: "p23 e2e active nebula revision two", TopK: 10,
	})
	if err != nil {
		t.Fatalf("search rebuilt active document: %v", err)
	}
	found := false
	for _, hit := range search.GetHits() {
		if hit.GetChunk().GetDocumentId() == active.Document.DocumentID && hit.GetChunk().GetVersionId() == active.Version.VersionID {
			found = true
		}
		if hit.GetChunk().GetDocumentId() == trashed.Document.DocumentID {
			t.Fatalf("trashed document returned from search: %#v", hit)
		}
	}
	if !found {
		t.Fatalf("rebuilt active document missing from search: %#v", search.GetHits())
	}
}
