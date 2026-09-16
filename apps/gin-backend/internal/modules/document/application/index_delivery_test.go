package application

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	domain "gin-backend/internal/modules/document/domain"
)

func TestIndexWorkerDeliversSyncInOrder(t *testing.T) {
	store := newIndexDeliveryStoreStub()
	event := testSyncDeliveryEvent()
	store.claimed = []*domain.IndexDeliveryEvent{event}
	store.version = &domain.DocumentVersion{
		VersionID: *event.VersionID, DocumentID: event.DocumentID, Title: "P2.3",
		Content: "reliable delivery", ContentFormat: "markdown", ContentSHA256: event.ContentSHA256,
	}
	client := &indexClientStub{}
	worker, err := NewIndexWorker(store, client, "worker-1", IndexWorkerConfig{Concurrency: 1})
	if err != nil {
		t.Fatalf("new worker: %v", err)
	}

	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatalf("run once: %v", err)
	}
	wantCalls := []string{
		event.OperationID("index"), event.OperationID("access"), event.OperationID("activate"),
	}
	if !reflect.DeepEqual(client.calls, wantCalls) {
		t.Fatalf("calls = %#v, want %#v", client.calls, wantCalls)
	}
	if store.succeeded != event.EventID {
		t.Fatalf("succeeded = %q, want %q", store.succeeded, event.EventID)
	}
	if store.renewed != 3 {
		t.Fatalf("lease renewals = %d, want 3", store.renewed)
	}
	if client.indexInput.ChunkSize != 180 || client.indexInput.Overlap != 20 {
		t.Fatalf("chunk profile = (%d,%d), want (180,20)", client.indexInput.ChunkSize, client.indexInput.Overlap)
	}
	if client.activateInput.ExpectedPreviousVersionID != "018f3f0e-7b20-7000-8000-000000000202" {
		t.Fatalf("previous version = %q", client.activateInput.ExpectedPreviousVersionID)
	}
}

func TestIndexWorkerReschedulesRetryableFailure(t *testing.T) {
	store := newIndexDeliveryStoreStub()
	event := testSyncDeliveryEvent()
	store.claimed = []*domain.IndexDeliveryEvent{event}
	store.version = &domain.DocumentVersion{VersionID: *event.VersionID, DocumentID: event.DocumentID, ContentSHA256: event.ContentSHA256}
	client := &indexClientStub{indexErr: &RemoteIndexError{Code: "Unavailable", Retryable: true, Err: errors.New("offline")}}
	worker, err := NewIndexWorker(store, client, "worker-1", IndexWorkerConfig{Concurrency: 1, BaseBackoff: time.Second, MaxBackoff: time.Minute})
	if err != nil {
		t.Fatalf("new worker: %v", err)
	}
	worker.now = func() time.Time { return time.Unix(1700000000, 0).UTC() }

	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatalf("run once: %v", err)
	}
	if store.retryCode != "Unavailable" {
		t.Fatalf("retry code = %q", store.retryCode)
	}
	if !store.retryAt.After(worker.now()) {
		t.Fatalf("retry time = %s, want after now", store.retryAt)
	}
	if store.deadCode != "" {
		t.Fatalf("unexpected dead letter code %q", store.deadCode)
	}
}

func TestIndexWorkerRetriesAmbiguousIndexWithSameOperationID(t *testing.T) {
	store := newIndexDeliveryStoreStub()
	event := testSyncDeliveryEvent()
	store.claimed = []*domain.IndexDeliveryEvent{event}
	store.version = &domain.DocumentVersion{VersionID: *event.VersionID, DocumentID: event.DocumentID, ContentSHA256: event.ContentSHA256}
	client := &indexClientStub{indexErrors: []error{
		&RemoteIndexError{Code: "DeadlineExceeded", Retryable: true, Err: context.DeadlineExceeded}, nil,
	}}
	worker, err := NewIndexWorker(store, client, "worker-1", IndexWorkerConfig{Concurrency: 1})
	if err != nil {
		t.Fatalf("new worker: %v", err)
	}

	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatalf("second run: %v", err)
	}
	want := []string{
		event.OperationID("index"), event.OperationID("index"),
		event.OperationID("access"), event.OperationID("activate"),
	}
	if !reflect.DeepEqual(client.calls, want) {
		t.Fatalf("calls = %#v, want %#v", client.calls, want)
	}
	if store.succeeded != event.EventID {
		t.Fatal("event was not completed after retry")
	}
}

func TestIndexWorkerDeadLettersSnapshotMismatch(t *testing.T) {
	store := newIndexDeliveryStoreStub()
	event := testSyncDeliveryEvent()
	store.claimed = []*domain.IndexDeliveryEvent{event}
	store.version = &domain.DocumentVersion{VersionID: *event.VersionID, DocumentID: event.DocumentID, ContentSHA256: "different"}
	client := &indexClientStub{}
	worker, err := NewIndexWorker(store, client, "worker-1", IndexWorkerConfig{Concurrency: 1})
	if err != nil {
		t.Fatalf("new worker: %v", err)
	}

	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatalf("run once: %v", err)
	}
	if store.deadCode != "LocalPermanent" {
		t.Fatalf("dead letter code = %q", store.deadCode)
	}
	if len(client.calls) != 0 {
		t.Fatalf("remote calls = %#v, want none", client.calls)
	}
}

func testSyncDeliveryEvent() *domain.IndexDeliveryEvent {
	versionID := "018f3f0e-7b20-7000-8000-000000000203"
	previousID := "018f3f0e-7b20-7000-8000-000000000202"
	leaseToken := "018f3f0e-7b20-7000-8000-000000000204"
	return &domain.IndexDeliveryEvent{
		EventID: "018f3f0e-7b20-7000-8000-000000000205", DocumentID: "018f3f0e-7b20-7000-8000-000000000201",
		Kind: domain.IndexDeliverySyncDocument, VersionID: &versionID, PreviousVersionID: &previousID,
		OwnerSpaceID: "018f3f0e-7b20-7000-8000-000000000206", ActivationRevision: 2,
		AccessRevision: 3, LifecycleRevision: 0, GrantedSpaceIDs: []string{"018f3f0e-7b20-7000-8000-000000000207"},
		ContentSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		IndexProfile:  domain.DefaultIndexProfile, AttemptCount: 1, LeaseToken: &leaseToken,
	}
}

type indexDeliveryStoreStub struct {
	claimed   []*domain.IndexDeliveryEvent
	version   *domain.DocumentVersion
	renewed   int
	succeeded string
	retryAt   time.Time
	retryCode string
	deadCode  string
}

func newIndexDeliveryStoreStub() *indexDeliveryStoreStub { return &indexDeliveryStoreStub{} }
func (store *indexDeliveryStoreStub) ClaimIndexDeliveryEvents(context.Context, string, int, time.Duration) ([]*domain.IndexDeliveryEvent, error) {
	return store.claimed, nil
}
func (store *indexDeliveryStoreStub) RenewIndexDeliveryLease(context.Context, string, string, string, time.Duration) error {
	store.renewed++
	return nil
}
func (store *indexDeliveryStoreStub) MarkIndexDeliverySucceeded(_ context.Context, eventID, _, _ string) error {
	store.succeeded = eventID
	return nil
}
func (store *indexDeliveryStoreStub) RescheduleIndexDelivery(_ context.Context, _, _, _ string, next time.Time, code, _ string) error {
	store.retryAt, store.retryCode = next, code
	return nil
}
func (store *indexDeliveryStoreStub) MarkIndexDeliveryDeadLetter(_ context.Context, _, _, _, code, _ string) error {
	store.deadCode = code
	return nil
}
func (store *indexDeliveryStoreStub) GetDocumentVersion(context.Context, string) (*domain.DocumentVersion, error) {
	return store.version, nil
}

type indexClientStub struct {
	calls         []string
	indexInput    IndexDocumentVersionInput
	activateInput ActivateDocumentVersionInput
	indexErr      error
	indexErrors   []error
}

func (client *indexClientStub) IndexDocumentVersion(_ context.Context, input IndexDocumentVersionInput) error {
	client.calls = append(client.calls, input.OperationID)
	client.indexInput = input
	if len(client.indexErrors) > 0 {
		err := client.indexErrors[0]
		client.indexErrors = client.indexErrors[1:]
		return err
	}
	return client.indexErr
}
func (client *indexClientStub) UpdateDocumentAccess(_ context.Context, input UpdateDocumentAccessInput) error {
	client.calls = append(client.calls, input.OperationID)
	return nil
}
func (client *indexClientStub) ActivateDocumentVersion(_ context.Context, input ActivateDocumentVersionInput) error {
	client.calls = append(client.calls, input.OperationID)
	client.activateInput = input
	return nil
}
func (client *indexClientStub) DeleteDocumentVersion(_ context.Context, input DeleteDocumentVersionInput) error {
	client.calls = append(client.calls, input.OperationID)
	return nil
}
func (client *indexClientStub) DeleteDocument(_ context.Context, input DeleteDocumentInput) error {
	client.calls = append(client.calls, input.OperationID)
	return nil
}
func (client *indexClientStub) GetDocumentVersionState(context.Context, string, string) (DocumentVersionIndexState, error) {
	return DocumentVersionIndexState{}, nil
}
