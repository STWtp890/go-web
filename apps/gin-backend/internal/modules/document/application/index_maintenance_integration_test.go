package application_test

import (
	"context"
	"os"
	"testing"

	application "gin-backend/internal/modules/document/application"
	domain "gin-backend/internal/modules/document/domain"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

func TestIndexMaintenanceIntegration(t *testing.T) {
	if os.Getenv("INDEX_MAINTENANCE_INTEGRATION") != "1" {
		t.Skip("INDEX_MAINTENANCE_INTEGRATION is not set")
	}
	db, store := openCommandRepository(t)
	ctx := context.Background()
	ownerID := createCommandUser(t, db, "index-maintenance")
	commands := newCommandService(t, store)
	created, err := commands.Create(ctx, application.CreateCommand{
		OwnerID: ownerID, Title: "P2.3 maintenance", Content: "reconcile and rebuild", AuthenticatedPublic: true,
	})
	if err != nil {
		t.Fatalf("create maintenance document: %v", err)
	}
	completeDocumentDeliveries(t, db, created.Document.DocumentID)

	client := &maintenanceIndexClientStub{states: map[string]domain.DocumentVersionIndexState{
		created.Document.DocumentID: {
			Exists: true, DocumentID: created.Document.DocumentID, VersionID: created.Version.VersionID,
			Status: "DOCUMENT_INDEX_STATUS_ACTIVE", ActivationRevision: 1, AccessRevision: 1,
			LifecycleRevision: 0, ContentSHA256: created.Version.ContentSHA256,
		},
	}}
	maintenance, err := application.NewIndexMaintenanceService(store, client)
	if err != nil {
		t.Fatalf("new maintenance service: %v", err)
	}

	if _, err := maintenance.Reconcile(ctx, 100); err != nil {
		t.Fatalf("reconcile matching state: %v", err)
	}
	assertDeliveryKind(t, db, created.Document.DocumentID, "reconcile", domain.IndexDeliverySyncAccess)
	completeDocumentDeliveries(t, db, created.Document.DocumentID)

	client.states[created.Document.DocumentID] = domain.DocumentVersionIndexState{}
	if _, err := maintenance.Reconcile(ctx, 100); err != nil {
		t.Fatalf("reconcile missing state: %v", err)
	}
	assertDeliveryKind(t, db, created.Document.DocumentID, "reconcile", domain.IndexDeliverySyncDocument)

	runID := uuid.NewString()
	run, err := maintenance.PrepareRebuild(ctx, runID)
	if err != nil {
		t.Fatalf("prepare rebuild: %v", err)
	}
	if run.State != domain.IndexRebuildRunning || run.EventCount == 0 {
		t.Fatalf("prepared run = %#v, want running with events", run)
	}
	assertDeliveryKind(t, db, created.Document.DocumentID, "rebuild", domain.IndexDeliverySyncDocument)
	if err := db.Exec(`UPDATE document_index_delivery_events
		SET state = 'succeeded', delivered_at = clock_timestamp(), lease_owner = NULL,
		    lease_token = NULL, lease_expires_at = NULL
		WHERE source_run_id = ?`, runID).Error; err != nil {
		t.Fatalf("complete rebuild deliveries: %v", err)
	}
	run, err = maintenance.RebuildStatus(ctx, runID)
	if err != nil {
		t.Fatalf("refresh rebuild status: %v", err)
	}
	if run.State != domain.IndexRebuildSucceeded || run.CompletedAt == nil || run.FailureCount != 0 {
		t.Fatalf("completed run = %#v", run)
	}
}

func completeDocumentDeliveries(t *testing.T, db *gorm.DB, documentID string) {
	t.Helper()
	if err := db.Exec(`UPDATE document_index_delivery_events
		SET state = 'succeeded', delivered_at = clock_timestamp(), lease_owner = NULL,
		    lease_token = NULL, lease_expires_at = NULL
		WHERE document_id = ? AND state <> 'succeeded'`, documentID).Error; err != nil {
		t.Fatalf("complete document deliveries: %v", err)
	}
}

func assertDeliveryKind(t *testing.T, db *gorm.DB, documentID, source string, want domain.IndexDeliveryKind) {
	t.Helper()
	var kind string
	if err := db.Table("document_index_delivery_events").Select("event_kind").
		Where("document_id = ? AND source = ?", documentID, source).
		Order("created_at DESC, event_id DESC").Limit(1).Scan(&kind).Error; err != nil {
		t.Fatalf("read %s delivery kind: %v", source, err)
	}
	if kind != string(want) {
		t.Fatalf("%s delivery kind = %q, want %q", source, kind, want)
	}
}

type maintenanceIndexClientStub struct {
	states map[string]domain.DocumentVersionIndexState
}

func (*maintenanceIndexClientStub) IndexDocumentVersion(context.Context, domain.IndexDocumentVersionInput) error {
	return nil
}
func (*maintenanceIndexClientStub) UpdateDocumentAccess(context.Context, domain.UpdateDocumentAccessInput) error {
	return nil
}
func (*maintenanceIndexClientStub) ActivateDocumentVersion(context.Context, domain.ActivateDocumentVersionInput) error {
	return nil
}
func (*maintenanceIndexClientStub) DeleteDocumentVersion(context.Context, domain.DeleteDocumentVersionInput) error {
	return nil
}
func (*maintenanceIndexClientStub) DeleteDocument(context.Context, domain.DeleteDocumentInput) error {
	return nil
}
func (client *maintenanceIndexClientStub) GetDocumentVersionState(_ context.Context, documentID, _ string) (domain.DocumentVersionIndexState, error) {
	return client.states[documentID], nil
}
