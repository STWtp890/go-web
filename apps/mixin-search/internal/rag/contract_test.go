package rag

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestIndexDocumentVersionNaturalKeyAndOwnerSpace(t *testing.T) {
	service := newContractTestService(t)
	ctx := context.Background()
	request := IndexDocumentVersionRequest{
		OperationID:       "index-v1",
		DocumentID:        "document",
		VersionID:         "v1",
		OwnerSpaceID:      "owner-space",
		LifecycleRevision: 0,
		Filename:          "document.md",
		Title:             "Document",
		Content:           []byte("naturalkeyneedle"),
	}

	first, err := service.IndexDocumentVersion(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.IndexDocumentVersion(ctx, request)
	if err != nil {
		t.Fatalf("idempotent index retry: %v", err)
	}
	if first.OwnerSpaceID != "owner-space" || second.ContentSHA256 != first.ContentSHA256 || second.ChunkCount != first.ChunkCount {
		t.Fatalf("index states differ: first=%+v second=%+v", first, second)
	}

	conflicting := request
	conflicting.OperationID = "index-v1-conflict"
	conflicting.Content = []byte("different content")
	if _, err = service.IndexDocumentVersion(ctx, conflicting); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting immutable version error = %v, want ErrConflict", err)
	}

	differentOwner := request
	differentOwner.OperationID = "index-v2-owner-conflict"
	differentOwner.VersionID = "v2"
	differentOwner.OwnerSpaceID = "another-owner-space"
	if _, err = service.IndexDocumentVersion(ctx, differentOwner); !errors.Is(err, ErrConflict) {
		t.Fatalf("different document owner space error = %v, want ErrConflict", err)
	}
}

func TestActivationFencing(t *testing.T) {
	service := newContractTestService(t)
	ctx := context.Background()
	mustIndexContractVersion(t, service, "document", "v1", "owner-space", 0, "versionone")
	mustIndexContractVersion(t, service, "document", "v2", "owner-space", 0, "versiontwo")

	firstRequest := ActivateDocumentVersionRequest{
		OperationID: "activate-v1", DocumentID: "document", VersionID: "v1", ActivationRevision: 1,
	}
	first, err := service.ActivateDocumentVersion(ctx, firstRequest)
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != DocumentVersionActive || first.ActivationRevision != 1 {
		t.Fatalf("first activation = %+v", first)
	}
	if _, err = service.ActivateDocumentVersion(ctx, firstRequest); err != nil {
		t.Fatalf("same activation retry: %v", err)
	}

	conflict := firstRequest
	conflict.OperationID = "activate-conflict"
	conflict.VersionID = "v2"
	if _, err = service.ActivateDocumentVersion(ctx, conflict); !errors.Is(err, ErrConflict) {
		t.Fatalf("same revision different version error = %v, want ErrConflict", err)
	}

	second, err := service.ActivateDocumentVersion(ctx, ActivateDocumentVersionRequest{
		OperationID: "activate-v2", DocumentID: "document", VersionID: "v2",
		ActivationRevision: 2, ExpectedPreviousVersionID: "v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.Status != DocumentVersionActive || second.ActivationRevision != 2 {
		t.Fatalf("second activation = %+v", second)
	}
	staleRequest := firstRequest
	staleRequest.OperationID = "activate-v1-stale"
	if _, err = service.ActivateDocumentVersion(ctx, staleRequest); !errors.Is(err, ErrStaleActivation) {
		t.Fatalf("stale activation error = %v, want ErrStaleActivation", err)
	}

	v1, err := service.GetDocumentVersionState(ctx, GetDocumentVersionStateRequest{DocumentID: "document", VersionID: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	if !v1.Exists || v1.State.Status != DocumentVersionIndexed || v1.State.ActivationRevision != 0 {
		t.Fatalf("previous version state = %+v", v1)
	}
}

func TestAccessFencingAndAuthorizationOR(t *testing.T) {
	service := newContractTestService(t)
	ctx := context.Background()
	mustIndexContractVersion(t, service, "access-document", "v1", "owner-space", 0, "authorizationneedle")
	mustActivateContractVersion(t, service, "access-document", "v1", 1, 0)

	assertContractHitCount(t, service, SearchDocumentsRequest{
		Query: "authorizationneedle", AllowedSpaceIDs: []string{"owner-space"}, TopK: 1,
	}, 1)
	assertContractHitCount(t, service, SearchDocumentsRequest{
		Query: "authorizationneedle", AllowedDocumentIDs: []string{"access-document"}, TopK: 1,
	}, 1)
	assertContractHitCount(t, service, SearchDocumentsRequest{
		Query: "authorizationneedle", AllowedSpaceIDs: []string{"unrelated"}, TopK: 1,
	}, 0)

	accessRequest := UpdateDocumentAccessRequest{
		OperationID: "access-1", DocumentID: "access-document", AccessRevision: 1,
		GrantedSpaceIDs: []string{" shared-space ", "shared-space"},
	}
	state, err := service.UpdateDocumentAccess(ctx, accessRequest)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.GrantedSpaceIDs) != 1 || state.GrantedSpaceIDs[0] != "shared-space" || state.AccessRevision != 1 {
		t.Fatalf("normalized access state = %+v", state)
	}
	if _, err = service.UpdateDocumentAccess(ctx, accessRequest); err != nil {
		t.Fatalf("same access retry: %v", err)
	}
	assertContractHitCount(t, service, SearchDocumentsRequest{
		Query: "authorizationneedle", AllowedSpaceIDs: []string{"shared-space"}, TopK: 1,
	}, 1)

	publicRequest := UpdateDocumentAccessRequest{
		OperationID: "access-2", DocumentID: "access-document", AccessRevision: 2,
		AuthenticatedPublic: true,
	}
	if _, err = service.UpdateDocumentAccess(ctx, publicRequest); err != nil {
		t.Fatal(err)
	}
	assertContractHitCount(t, service, SearchDocumentsRequest{Query: "authorizationneedle", TopK: 1}, 1)

	conflict := publicRequest
	conflict.OperationID = "access-2-conflict"
	conflict.AuthenticatedPublic = false
	if _, err = service.UpdateDocumentAccess(ctx, conflict); !errors.Is(err, ErrConflict) {
		t.Fatalf("same access revision different snapshot error = %v, want ErrConflict", err)
	}

	revokeRequest := UpdateDocumentAccessRequest{
		OperationID: "access-3-revoke", DocumentID: "access-document", AccessRevision: 3,
	}
	if _, err = service.UpdateDocumentAccess(ctx, revokeRequest); err != nil {
		t.Fatal(err)
	}
	lateRequest := publicRequest
	lateRequest.OperationID = "access-2-late"
	if _, err = service.UpdateDocumentAccess(ctx, lateRequest); !errors.Is(err, ErrStaleAccess) {
		t.Fatalf("late authorization error = %v, want ErrStaleAccess", err)
	}
	assertContractHitCount(t, service, SearchDocumentsRequest{Query: "authorizationneedle", TopK: 1}, 0)
	assertContractHitCount(t, service, SearchDocumentsRequest{
		Query: "authorizationneedle", AllowedSpaceIDs: []string{"shared-space"}, TopK: 1,
	}, 0)
}

func TestLifecycleTombstoneFencingAndRepublish(t *testing.T) {
	service := newContractTestService(t)
	ctx := context.Background()
	mustIndexContractVersion(t, service, "lifecycle-document", "v1", "owner-space", 0, "lifecycleoldneedle")
	if _, err := service.UpdateDocumentAccess(ctx, UpdateDocumentAccessRequest{
		OperationID: "access-old", DocumentID: "lifecycle-document", AccessRevision: 1,
		AuthenticatedPublic: true,
	}); err != nil {
		t.Fatal(err)
	}
	mustActivateContractVersion(t, service, "lifecycle-document", "v1", 4, 0)

	deleted, err := service.DeleteDocument(ctx, DeleteDocumentRequest{
		OperationID: "delete-1", DocumentID: "lifecycle-document", LifecycleRevision: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !deleted.Tombstoned || deleted.LifecycleRevision != 1 {
		t.Fatalf("delete result = %+v", deleted)
	}
	assertContractHitCount(t, service, SearchDocumentsRequest{Query: "lifecycleoldneedle", TopK: 1}, 0)

	_, err = service.IndexDocumentVersion(ctx, IndexDocumentVersionRequest{
		OperationID: "late-index", DocumentID: "lifecycle-document", VersionID: "late",
		OwnerSpaceID: "owner-space", LifecycleRevision: 0, Filename: "late.md", Content: []byte("late"),
	})
	if !errors.Is(err, ErrStaleLifecycle) {
		t.Fatalf("late index error = %v, want ErrStaleLifecycle", err)
	}
	_, err = service.ActivateDocumentVersion(ctx, ActivateDocumentVersionRequest{
		OperationID: "late-activate", DocumentID: "lifecycle-document", VersionID: "v1",
		ActivationRevision: 5, LifecycleRevision: 0,
	})
	if !errors.Is(err, ErrStaleLifecycle) {
		t.Fatalf("late activation error = %v, want ErrStaleLifecycle", err)
	}
	_, err = service.UpdateDocumentAccess(ctx, UpdateDocumentAccessRequest{
		OperationID: "late-access", DocumentID: "lifecycle-document", AccessRevision: 2,
		LifecycleRevision: 0, AuthenticatedPublic: true,
	})
	if !errors.Is(err, ErrStaleLifecycle) {
		t.Fatalf("late access error = %v, want ErrStaleLifecycle", err)
	}

	mustIndexContractVersion(t, service, "lifecycle-document", "v2", "owner-space", 2, "lifecyclenewneedle")
	access, err := service.UpdateDocumentAccess(ctx, UpdateDocumentAccessRequest{
		OperationID: "access-new", DocumentID: "lifecycle-document", AccessRevision: 2,
		LifecycleRevision: 2, GrantedSpaceIDs: []string{"shared-space"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if access.LifecycleRevision != 2 || access.AccessRevision != 2 {
		t.Fatalf("republish access state = %+v", access)
	}
	republished := mustActivateContractVersion(t, service, "lifecycle-document", "v2", 5, 2)
	if republished.ActivationRevision != 5 || republished.AccessRevision != 2 || republished.LifecycleRevision != 2 {
		t.Fatalf("republished state = %+v", republished)
	}
	assertContractHitCount(t, service, SearchDocumentsRequest{
		Query: "lifecyclenewneedle", AllowedSpaceIDs: []string{"shared-space"}, TopK: 1,
	}, 1)

	if _, err = service.DeleteDocument(ctx, DeleteDocumentRequest{
		OperationID: "delete-1", DocumentID: "lifecycle-document", LifecycleRevision: 1,
	}); err != nil {
		t.Fatalf("same operation delete retry after republish: %v", err)
	}
	assertContractHitCount(t, service, SearchDocumentsRequest{
		Query: "lifecyclenewneedle", AllowedSpaceIDs: []string{"shared-space"}, TopK: 1,
	}, 1)

	if _, err = service.DeleteDocument(ctx, DeleteDocumentRequest{
		OperationID: "delete-1-retry", DocumentID: "lifecycle-document", LifecycleRevision: 1,
	}); err != nil {
		t.Fatalf("old delete natural-key retry after republish: %v", err)
	}
	assertContractHitCount(t, service, SearchDocumentsRequest{
		Query: "lifecyclenewneedle", AllowedSpaceIDs: []string{"shared-space"}, TopK: 1,
	}, 1)

	if _, err = service.ActivateDocumentVersion(ctx, ActivateDocumentVersionRequest{
		OperationID: "stale-content", DocumentID: "lifecycle-document", VersionID: "v1",
		ActivationRevision: 4, LifecycleRevision: 2,
	}); !errors.Is(err, ErrStaleActivation) {
		t.Fatalf("stale content error = %v, want ErrStaleActivation", err)
	}
	if _, err = service.UpdateDocumentAccess(ctx, UpdateDocumentAccessRequest{
		OperationID: "stale-access", DocumentID: "lifecycle-document", AccessRevision: 1,
		LifecycleRevision: 2, AuthenticatedPublic: true,
	}); !errors.Is(err, ErrStaleAccess) {
		t.Fatalf("stale access error = %v, want ErrStaleAccess", err)
	}

	if _, err = service.DeleteDocument(ctx, DeleteDocumentRequest{
		OperationID: "delete-3", DocumentID: "lifecycle-document", LifecycleRevision: 3,
	}); err != nil {
		t.Fatal(err)
	}
	assertContractHitCount(t, service, SearchDocumentsRequest{
		Query: "lifecyclenewneedle", AllowedSpaceIDs: []string{"shared-space"}, TopK: 1,
	}, 0)
}

func TestDeleteUnknownDocumentLeavesTombstone(t *testing.T) {
	service := newContractTestService(t)
	ctx := context.Background()
	deleted, err := service.DeleteDocument(ctx, DeleteDocumentRequest{
		OperationID: "delete-unknown", DocumentID: "unknown-document", LifecycleRevision: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !deleted.Tombstoned || deleted.LifecycleRevision != 7 {
		t.Fatalf("unknown delete = %+v", deleted)
	}

	_, err = service.IndexDocumentVersion(ctx, IndexDocumentVersionRequest{
		OperationID: "late-index", DocumentID: "unknown-document", VersionID: "v1",
		OwnerSpaceID: "owner-space", LifecycleRevision: 7, Filename: "unknown.md", Content: []byte("unknownneedle"),
	})
	if !errors.Is(err, ErrStaleLifecycle) {
		t.Fatalf("same lifecycle index error = %v, want ErrStaleLifecycle", err)
	}

	mustIndexContractVersion(t, service, "unknown-document", "v1", "owner-space", 8, "unknownneedle")
	mustActivateContractVersion(t, service, "unknown-document", "v1", 1, 8)
	assertContractHitCount(t, service, SearchDocumentsRequest{
		Query: "unknownneedle", AllowedSpaceIDs: []string{"owner-space"}, TopK: 1,
	}, 1)
	if _, err = service.DeleteDocument(ctx, DeleteDocumentRequest{
		OperationID: "delete-unknown-retry", DocumentID: "unknown-document", LifecycleRevision: 7,
	}); err != nil {
		t.Fatalf("unknown delete retry after republish: %v", err)
	}
	assertContractHitCount(t, service, SearchDocumentsRequest{
		Query: "unknownneedle", AllowedSpaceIDs: []string{"owner-space"}, TopK: 1,
	}, 1)
}

func TestRepublishSameVersionAtHigherLifecycleWithoutActivationAdvance(t *testing.T) {
	service := newContractTestService(t)
	ctx := context.Background()
	mustIndexContractVersion(t, service, "same-version", "v1", "owner-space", 0, "sameversionneedle")
	mustActivateContractVersion(t, service, "same-version", "v1", 1, 0)
	if _, err := service.DeleteDocument(ctx, DeleteDocumentRequest{
		OperationID: "delete-same-version", DocumentID: "same-version", LifecycleRevision: 1,
	}); err != nil {
		t.Fatal(err)
	}

	mustIndexContractVersion(t, service, "same-version", "v1", "owner-space", 2, "sameversionneedle")
	republished := mustActivateContractVersion(t, service, "same-version", "v1", 1, 2)
	if republished.Status != DocumentVersionActive || republished.ActivationRevision != 1 || republished.LifecycleRevision != 2 {
		t.Fatalf("same-version republish state = %+v", republished)
	}
	assertContractHitCount(t, service, SearchDocumentsRequest{
		Query: "sameversionneedle", AllowedSpaceIDs: []string{"owner-space"}, TopK: 1,
	}, 1)

	if _, err := service.ActivateDocumentVersion(ctx, ActivateDocumentVersionRequest{
		OperationID: "late-old-lifecycle", DocumentID: "same-version", VersionID: "v1",
		ActivationRevision: 1, LifecycleRevision: 1,
	}); !errors.Is(err, ErrStaleLifecycle) {
		t.Fatalf("old lifecycle activation error = %v, want ErrStaleLifecycle", err)
	}
}

func TestDeleteDocumentVersionRetryDoesNotDeleteRepublishedVersion(t *testing.T) {
	service := newContractTestService(t)
	ctx := context.Background()
	mustIndexContractVersion(t, service, "version-delete", "v1", "owner-space", 0, "versiondeleteneedle")
	mustActivateContractVersion(t, service, "version-delete", "v1", 1, 0)
	request := DeleteDocumentVersionRequest{
		OperationID: "delete-version", DocumentID: "version-delete", VersionID: "v1",
	}
	result, err := service.DeleteDocumentVersion(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Deleted || !result.ActiveVersionRemoved {
		t.Fatalf("version delete result = %+v", result)
	}
	if _, err = service.IndexDocumentVersion(ctx, IndexDocumentVersionRequest{
		OperationID: "reindex-version-conflict", DocumentID: "version-delete", VersionID: "v1",
		OwnerSpaceID: "owner-space", Filename: "v1.md", Content: []byte("changed-after-delete"),
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed version after delete error = %v, want ErrConflict", err)
	}

	if _, err = service.IndexDocumentVersion(ctx, IndexDocumentVersionRequest{
		OperationID: "reindex-version-same-content", DocumentID: "version-delete", VersionID: "v1",
		OwnerSpaceID: "owner-space", Filename: "v1.md", Content: []byte("versiondeleteneedle"),
	}); err != nil {
		t.Fatalf("reindex immutable version: %v", err)
	}
	mustActivateContractVersion(t, service, "version-delete", "v1", 2, 0)
	if _, err = service.DeleteDocumentVersion(ctx, request); err != nil {
		t.Fatalf("same operation delete retry: %v", err)
	}
	request.OperationID = "delete-version-natural-key-retry"
	if _, err = service.DeleteDocumentVersion(ctx, request); err != nil {
		t.Fatalf("natural-key delete retry: %v", err)
	}
	assertContractHitCount(t, service, SearchDocumentsRequest{
		Query: "versiondeleteneedle", AllowedSpaceIDs: []string{"owner-space"}, TopK: 1,
	}, 1)
}

func TestOperationIDBindingAndExactReplay(t *testing.T) {
	service := newContractTestService(t)
	ctx := context.Background()
	indexRequest := IndexDocumentVersionRequest{
		OperationID: "operation-index", DocumentID: "operation-document", VersionID: "v1",
		OwnerSpaceID: "owner-space", Filename: "operation.md", Content: []byte("operationneedle"),
	}
	indexed, err := service.IndexDocumentVersion(ctx, indexRequest)
	if err != nil {
		t.Fatal(err)
	}
	mustActivateContractVersion(t, service, "operation-document", "v1", 1, 0)
	replayed, err := service.IndexDocumentVersion(ctx, indexRequest)
	if err != nil {
		t.Fatal(err)
	}
	if replayed != indexed || replayed.Status != DocumentVersionIndexed {
		t.Fatalf("index operation replay = %+v, want original %+v", replayed, indexed)
	}

	changedIndex := indexRequest
	changedIndex.Filename = "changed.md"
	if _, err = service.IndexDocumentVersion(ctx, changedIndex); !errors.Is(err, ErrConflict) {
		t.Fatalf("rebound index operation error = %v, want ErrConflict", err)
	}
	if _, err = service.UpdateDocumentAccess(ctx, UpdateDocumentAccessRequest{
		OperationID: "operation-index", DocumentID: "operation-document", AccessRevision: 1,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("cross-RPC operation reuse error = %v, want ErrConflict", err)
	}

	accessRequest := UpdateDocumentAccessRequest{
		OperationID: "operation-access", DocumentID: "operation-document", AccessRevision: 1,
		GrantedSpaceIDs: []string{"shared-space"},
	}
	access, err := service.UpdateDocumentAccess(ctx, accessRequest)
	if err != nil {
		t.Fatal(err)
	}
	access.GrantedSpaceIDs[0] = "caller-mutation"
	replayedAccess, err := service.UpdateDocumentAccess(ctx, accessRequest)
	if err != nil {
		t.Fatal(err)
	}
	if len(replayedAccess.GrantedSpaceIDs) != 1 || replayedAccess.GrantedSpaceIDs[0] != "shared-space" {
		t.Fatalf("access operation replay was mutated by caller: %+v", replayedAccess)
	}
	changedAccess := accessRequest
	changedAccess.AccessRevision = 2
	if _, err = service.UpdateDocumentAccess(ctx, changedAccess); !errors.Is(err, ErrConflict) {
		t.Fatalf("rebound access operation error = %v, want ErrConflict", err)
	}
}

func TestDocumentIndexServiceConcurrentIdempotentAccessAndSearch(t *testing.T) {
	service := newContractTestService(t)
	ctx := context.Background()
	mustIndexContractVersion(t, service, "concurrent", "v1", "owner-space", 0, "concurrentneedle")
	mustActivateContractVersion(t, service, "concurrent", "v1", 1, 0)
	request := UpdateDocumentAccessRequest{
		OperationID: "concurrent-access", DocumentID: "concurrent", AccessRevision: 1,
		AuthenticatedPublic: true,
	}
	if _, err := service.UpdateDocumentAccess(ctx, request); err != nil {
		t.Fatal(err)
	}

	const workers = 24
	errorsChannel := make(chan error, workers)
	var wait sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wait.Add(1)
		go func(search bool) {
			defer wait.Done()
			if search {
				result, err := service.SearchDocuments(ctx, SearchDocumentsRequest{Query: "concurrentneedle", TopK: 1})
				if err != nil {
					errorsChannel <- err
					return
				}
				if len(result.Hits) != 1 {
					errorsChannel <- fmt.Errorf("concurrent search hits = %d, want 1", len(result.Hits))
				}
				return
			}
			if _, err := service.UpdateDocumentAccess(ctx, request); err != nil {
				errorsChannel <- err
			}
		}(worker%2 == 0)
	}
	wait.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		t.Error(err)
	}
}

func newContractTestService(t *testing.T) *DocumentIndexService {
	t.Helper()
	core, err := NewService(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := core.Close(); err != nil {
			t.Errorf("close core: %v", err)
		}
	})
	service, err := NewDocumentIndexService(core)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func mustIndexContractVersion(
	t *testing.T,
	service *DocumentIndexService,
	documentID string,
	versionID string,
	ownerSpaceID string,
	lifecycleRevision uint64,
	content string,
) DocumentVersionState {
	t.Helper()
	state, err := service.IndexDocumentVersion(context.Background(), IndexDocumentVersionRequest{
		OperationID:       fmt.Sprintf("index-%s-%s-%d", documentID, versionID, lifecycleRevision),
		DocumentID:        documentID,
		VersionID:         versionID,
		OwnerSpaceID:      ownerSpaceID,
		LifecycleRevision: lifecycleRevision,
		Filename:          versionID + ".md",
		Content:           []byte(content),
	})
	if err != nil {
		t.Fatalf("index %s/%s: %v", documentID, versionID, err)
	}
	return state
}

func mustActivateContractVersion(
	t *testing.T,
	service *DocumentIndexService,
	documentID string,
	versionID string,
	activationRevision uint64,
	lifecycleRevision uint64,
) DocumentVersionState {
	t.Helper()
	state, err := service.ActivateDocumentVersion(context.Background(), ActivateDocumentVersionRequest{
		OperationID:        fmt.Sprintf("activate-%s-%s-%d-%d", documentID, versionID, activationRevision, lifecycleRevision),
		DocumentID:         documentID,
		VersionID:          versionID,
		ActivationRevision: activationRevision,
		LifecycleRevision:  lifecycleRevision,
	})
	if err != nil {
		t.Fatalf("activate %s/%s: %v", documentID, versionID, err)
	}
	return state
}

func assertContractHitCount(
	t *testing.T,
	service *DocumentIndexService,
	request SearchDocumentsRequest,
	want int,
) {
	t.Helper()
	result, err := service.SearchDocuments(context.Background(), request)
	if err != nil {
		t.Fatalf("search %q: %v", request.Query, err)
	}
	if len(result.Hits) != want {
		t.Fatalf("search %q hits = %d, want %d; hits=%+v", request.Query, len(result.Hits), want, result.Hits)
	}
}
