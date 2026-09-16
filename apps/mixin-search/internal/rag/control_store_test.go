package rag

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
)

func TestControlStateSurvivesRestartAndReplaysFirstResponse(t *testing.T) {
	ctx := context.Background()
	vectorStore := NewMemoryStore()
	controlStore := NewMemoryControlStore()
	first := newControlTestService(t, vectorStore, controlStore)
	indexRequest := IndexDocumentVersionRequest{
		OperationID:       "restart-index",
		DocumentID:        "restart-document",
		VersionID:         "v1",
		OwnerSpaceID:      "owner-space",
		LifecycleRevision: 1,
		Filename:          "restart.md",
		Content:           []byte("restartpersistenceneedle"),
		SourceURI:         "document://restart/v1",
		Metadata:          map[string]string{"category": "p2.1"},
	}
	indexed, err := first.IndexDocumentVersion(ctx, indexRequest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.ActivateDocumentVersion(ctx, ActivateDocumentVersionRequest{
		OperationID:        "restart-activate",
		DocumentID:         indexRequest.DocumentID,
		VersionID:          indexRequest.VersionID,
		ActivationRevision: 1,
		LifecycleRevision:  1,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := first.UpdateDocumentAccess(ctx, UpdateDocumentAccessRequest{
		OperationID:       "restart-access",
		DocumentID:        indexRequest.DocumentID,
		AccessRevision:    2,
		LifecycleRevision: 1,
		GrantedSpaceIDs:   []string{"shared-space"},
	}); err != nil {
		t.Fatal(err)
	}

	restarted := newControlTestService(t, vectorStore, controlStore)
	version, err := restarted.GetDocumentVersionState(ctx, GetDocumentVersionStateRequest{
		DocumentID: indexRequest.DocumentID,
		VersionID:  indexRequest.VersionID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !version.Exists || version.State.Status != DocumentVersionActive || version.State.ActivationRevision != 1 ||
		version.State.AccessRevision != 2 || version.State.LifecycleRevision != 1 {
		t.Fatalf("restored version = %+v", version)
	}
	hits, err := restarted.SearchDocuments(ctx, SearchDocumentsRequest{
		Query:           "restartpersistenceneedle",
		AllowedSpaceIDs: []string{"shared-space"},
		TopK:            1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits.Hits) != 1 || hits.Hits[0].Chunk.Source != indexRequest.SourceURI ||
		hits.Hits[0].Chunk.Metadata["category"] != "p2.1" {
		t.Fatalf("restored search hits = %+v", hits.Hits)
	}

	replayed, err := restarted.IndexDocumentVersion(ctx, indexRequest)
	if err != nil {
		t.Fatal(err)
	}
	if replayed != indexed || replayed.Status != DocumentVersionIndexed {
		t.Fatalf("replayed index = %+v, want first response %+v", replayed, indexed)
	}
	rebound := indexRequest
	rebound.Title = "rebound payload"
	if _, err := restarted.IndexDocumentVersion(ctx, rebound); !errors.Is(err, ErrConflict) {
		t.Fatalf("rebound operation error = %v, want ErrConflict", err)
	}
	if _, err := restarted.UpdateDocumentAccess(ctx, UpdateDocumentAccessRequest{
		OperationID: "restart-stale-access", DocumentID: indexRequest.DocumentID, AccessRevision: 1, LifecycleRevision: 1,
	}); !errors.Is(err, ErrStaleAccess) {
		t.Fatalf("restored access high-water error = %v, want ErrStaleAccess", err)
	}
	if _, err := restarted.UpdateDocumentAccess(ctx, UpdateDocumentAccessRequest{
		OperationID: "restart-conflicting-access", DocumentID: indexRequest.DocumentID, AccessRevision: 2,
		LifecycleRevision: 1, AuthenticatedPublic: true,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("restored access same-revision error = %v, want ErrConflict", err)
	}
}

func TestRestartReplaysEveryOperationResult(t *testing.T) {
	ctx := context.Background()
	vectorStore := NewMemoryStore()
	controlStore := NewMemoryControlStore()
	service := newControlTestService(t, vectorStore, controlStore)
	indexV1 := IndexDocumentVersionRequest{
		OperationID: "all-index-v1", DocumentID: "all-operations", VersionID: "v1",
		OwnerSpaceID: "owner", LifecycleRevision: 1, Filename: "v1.md", Content: []byte("version one"),
	}
	indexV2 := IndexDocumentVersionRequest{
		OperationID: "all-index-v2", DocumentID: "all-operations", VersionID: "v2",
		OwnerSpaceID: "owner", LifecycleRevision: 1, Filename: "v2.md", Content: []byte("version two"),
	}
	activate := ActivateDocumentVersionRequest{
		OperationID: "all-activate", DocumentID: "all-operations", VersionID: "v1",
		ActivationRevision: 1, LifecycleRevision: 1,
	}
	access := UpdateDocumentAccessRequest{
		OperationID: "all-access", DocumentID: "all-operations", AccessRevision: 1,
		LifecycleRevision: 1, GrantedSpaceIDs: []string{"shared"},
	}
	deleteVersion := DeleteDocumentVersionRequest{
		OperationID: "all-delete-version", DocumentID: "all-operations", VersionID: "v2", LifecycleRevision: 1,
	}
	deleteDocument := DeleteDocumentRequest{
		OperationID: "all-delete-document", DocumentID: "all-operations", LifecycleRevision: 2,
	}
	wantIndexV1, err := service.IndexDocumentVersion(ctx, indexV1)
	if err != nil {
		t.Fatal(err)
	}
	wantIndexV2, err := service.IndexDocumentVersion(ctx, indexV2)
	if err != nil {
		t.Fatal(err)
	}
	wantActivate, err := service.ActivateDocumentVersion(ctx, activate)
	if err != nil {
		t.Fatal(err)
	}
	wantAccess, err := service.UpdateDocumentAccess(ctx, access)
	if err != nil {
		t.Fatal(err)
	}
	wantDeleteVersion, err := service.DeleteDocumentVersion(ctx, deleteVersion)
	if err != nil {
		t.Fatal(err)
	}
	wantDeleteDocument, err := service.DeleteDocument(ctx, deleteDocument)
	if err != nil {
		t.Fatal(err)
	}

	restarted := newControlTestService(t, vectorStore, controlStore)
	if got, err := restarted.IndexDocumentVersion(ctx, indexV1); err != nil || got != wantIndexV1 {
		t.Fatalf("index v1 replay=%+v error=%v want=%+v", got, err, wantIndexV1)
	}
	if got, err := restarted.IndexDocumentVersion(ctx, indexV2); err != nil || got != wantIndexV2 {
		t.Fatalf("index v2 replay=%+v error=%v want=%+v", got, err, wantIndexV2)
	}
	if got, err := restarted.ActivateDocumentVersion(ctx, activate); err != nil || got != wantActivate {
		t.Fatalf("activate replay=%+v error=%v want=%+v", got, err, wantActivate)
	}
	if got, err := restarted.UpdateDocumentAccess(ctx, access); err != nil || !reflect.DeepEqual(got, wantAccess) {
		t.Fatalf("access replay=%+v error=%v want=%+v", got, err, wantAccess)
	}
	if got, err := restarted.DeleteDocumentVersion(ctx, deleteVersion); err != nil || got != wantDeleteVersion {
		t.Fatalf("delete version replay=%+v error=%v want=%+v", got, err, wantDeleteVersion)
	}
	if got, err := restarted.DeleteDocument(ctx, deleteDocument); err != nil || got != wantDeleteDocument {
		t.Fatalf("delete document replay=%+v error=%v want=%+v", got, err, wantDeleteDocument)
	}
}

func TestControlStateTombstoneSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	vectorStore := NewMemoryStore()
	controlStore := NewMemoryControlStore()
	service := newControlTestService(t, vectorStore, controlStore)
	mustIndexContractVersion(t, service, "tombstone-restart", "v1", "owner-space", 1, "tombstonerestartneedle")
	mustActivateContractVersion(t, service, "tombstone-restart", "v1", 1, 1)
	if _, err := service.UpdateDocumentAccess(ctx, UpdateDocumentAccessRequest{
		OperationID:         "tombstone-public",
		DocumentID:          "tombstone-restart",
		AccessRevision:      1,
		LifecycleRevision:   1,
		AuthenticatedPublic: true,
	}); err != nil {
		t.Fatal(err)
	}
	deleteRequest := DeleteDocumentRequest{
		OperationID:       "tombstone-delete",
		DocumentID:        "tombstone-restart",
		LifecycleRevision: 2,
	}
	deleted, err := service.DeleteDocument(ctx, deleteRequest)
	if err != nil {
		t.Fatal(err)
	}

	restarted := newControlTestService(t, vectorStore, controlStore)
	assertContractHitCount(t, restarted, SearchDocumentsRequest{Query: "tombstonerestartneedle", TopK: 1}, 0)
	replayed, err := restarted.DeleteDocument(ctx, deleteRequest)
	if err != nil {
		t.Fatal(err)
	}
	if replayed != deleted {
		t.Fatalf("replayed delete = %+v, want %+v", replayed, deleted)
	}
	if _, err := restarted.IndexDocumentVersion(ctx, IndexDocumentVersionRequest{
		OperationID:       "tombstone-late-index",
		DocumentID:        "tombstone-restart",
		VersionID:         "late",
		OwnerSpaceID:      "owner-space",
		LifecycleRevision: 1,
		Filename:          "late.md",
		Content:           []byte("late"),
	}); !errors.Is(err, ErrStaleLifecycle) {
		t.Fatalf("late index error = %v, want ErrStaleLifecycle", err)
	}
}

func TestMemoryControlStoreCompareAndSwap(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryControlStore()
	left, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	right, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	left.Manifests["deep-copy"] = ControlDocumentManifest{GrantedSpaceIDs: []string{"space"}}
	if generation, err := store.Save(ctx, left.Generation, left); err != nil || generation != 1 {
		t.Fatalf("first save generation=%d error=%v", generation, err)
	}
	left.Manifests["deep-copy"].GrantedSpaceIDs[0] = "caller-mutation"
	loaded, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Manifests["deep-copy"].GrantedSpaceIDs[0]; got != "space" {
		t.Fatalf("saved state was mutated through caller input: %q", got)
	}
	loaded.Manifests["deep-copy"].GrantedSpaceIDs[0] = "loaded-mutation"
	loadedAgain, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := loadedAgain.Manifests["deep-copy"].GrantedSpaceIDs[0]; got != "space" {
		t.Fatalf("saved state was mutated through loaded snapshot: %q", got)
	}
	if _, err := store.Save(ctx, right.Generation, right); !errors.Is(err, ErrControlStoreConflict) {
		t.Fatalf("stale save error = %v, want ErrControlStoreConflict", err)
	}
}

func TestAmbiguousVectorCommitIsCleanedFromDurableIntent(t *testing.T) {
	ctx := context.Background()
	vectorStore := newIngestCommitErrorStore()
	controlStore := NewMemoryControlStore()
	service := newControlTestService(t, vectorStore, controlStore)
	vectorStore.failNextIngest(errors.New("connection lost after vector commit"))
	_, err := service.IndexDocumentVersion(ctx, IndexDocumentVersionRequest{
		OperationID: "ambiguous-vector", DocumentID: "ambiguous-vector", VersionID: "v1",
		OwnerSpaceID: "owner", Filename: "ambiguous.md", Content: []byte("ambiguousvectorneedle"),
	})
	if err == nil {
		t.Fatal("ambiguous vector commit should return its storage error")
	}
	state, loadErr := controlStore.Load(ctx)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if len(state.PendingVectorWrites) != 0 || len(state.VersionsByKey) != 0 {
		t.Fatalf("control state after cleanup = %+v", state)
	}
	if len(vectorStore.inner.snapshot()) != 0 {
		t.Fatalf("orphan vector chunks = %d, want 0", len(vectorStore.inner.snapshot()))
	}
}

func TestConcurrentControlWritesRequireReloadBeforeRetry(t *testing.T) {
	ctx := context.Background()
	vectorStore := NewMemoryStore()
	controlStore := newFirstSavesBarrierControlStore(NewMemoryControlStore(), 2)
	left := newControlTestService(t, vectorStore, controlStore)
	right := newControlTestService(t, vectorStore, controlStore)
	requests := []IndexDocumentVersionRequest{
		{OperationID: "concurrent-left", DocumentID: "left", VersionID: "v1", OwnerSpaceID: "owner", Filename: "left.md", Content: []byte("left")},
		{OperationID: "concurrent-right", DocumentID: "right", VersionID: "v1", OwnerSpaceID: "owner", Filename: "right.md", Content: []byte("right")},
	}
	type response struct {
		index int
		err   error
	}
	responses := make(chan response, 2)
	go func() {
		_, err := left.IndexDocumentVersion(ctx, requests[0])
		responses <- response{index: 0, err: err}
	}()
	go func() {
		_, err := right.IndexDocumentVersion(ctx, requests[1])
		responses <- response{index: 1, err: err}
	}()

	conflicts := 0
	loser := -1
	for range 2 {
		result := <-responses
		if errors.Is(result.err, ErrControlStoreConflict) {
			conflicts++
			loser = result.index
			continue
		}
		if result.err != nil {
			t.Fatalf("concurrent write %d: %v", result.index, result.err)
		}
	}
	if conflicts != 1 {
		t.Fatalf("control conflicts = %d, want 1", conflicts)
	}
	retryService := left
	if loser == 1 {
		retryService = right
	}
	if _, err := retryService.IndexDocumentVersion(ctx, requests[loser]); err != nil {
		t.Fatalf("retry after reload: %v", err)
	}

	observer := newControlTestService(t, vectorStore, controlStore)
	for _, request := range requests {
		state, err := observer.GetDocumentVersionState(ctx, GetDocumentVersionStateRequest{
			DocumentID: request.DocumentID,
			VersionID:  request.VersionID,
		})
		if err != nil || !state.Exists {
			t.Fatalf("state %s exists=%v error=%v", request.DocumentID, state.Exists, err)
		}
	}
}

func TestRequestRefreshObservesExternalAccessRevocationAndTombstone(t *testing.T) {
	ctx := context.Background()
	vectorStore := NewMemoryStore()
	controlStore := NewMemoryControlStore()
	writer := newControlTestService(t, vectorStore, controlStore)
	mustIndexContractVersion(t, writer, "external-refresh", "v1", "owner", 1, "externalrefreshneedle")
	mustActivateContractVersion(t, writer, "external-refresh", "v1", 1, 1)
	if _, err := writer.UpdateDocumentAccess(ctx, UpdateDocumentAccessRequest{
		OperationID: "external-public", DocumentID: "external-refresh", AccessRevision: 1,
		LifecycleRevision: 1, AuthenticatedPublic: true,
	}); err != nil {
		t.Fatal(err)
	}
	observer := newControlTestService(t, vectorStore, controlStore)
	assertContractHitCount(t, observer, SearchDocumentsRequest{Query: "externalrefreshneedle", TopK: 1}, 1)
	if _, err := writer.UpdateDocumentAccess(ctx, UpdateDocumentAccessRequest{
		OperationID: "external-private", DocumentID: "external-refresh", AccessRevision: 2, LifecycleRevision: 1,
	}); err != nil {
		t.Fatal(err)
	}
	assertContractHitCount(t, observer, SearchDocumentsRequest{Query: "externalrefreshneedle", TopK: 1}, 0)
	if _, err := writer.DeleteDocument(ctx, DeleteDocumentRequest{
		OperationID: "external-delete", DocumentID: "external-refresh", LifecycleRevision: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := observer.IndexDocumentVersion(ctx, IndexDocumentVersionRequest{
		OperationID: "external-late", DocumentID: "external-refresh", VersionID: "late",
		OwnerSpaceID: "owner", LifecycleRevision: 1, Filename: "late.md", Content: []byte("late"),
	}); !errors.Is(err, ErrStaleLifecycle) {
		t.Fatalf("external tombstone late event = %v, want ErrStaleLifecycle", err)
	}
}

func TestControlStoreFailuresFailClosedAndRecoverAmbiguousCommit(t *testing.T) {
	ctx := context.Background()
	loadFailure := &faultControlStore{inner: NewMemoryControlStore(), loadErr: errors.New("load unavailable")}
	core, err := NewService(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })
	if _, err := NewDocumentIndexServiceWithControlStore(ctx, core, loadFailure); !errors.Is(err, ErrControlStoreUnavailable) {
		t.Fatalf("startup load error = %v, want ErrControlStoreUnavailable", err)
	}

	vectorStore := NewMemoryStore()
	faults := &faultControlStore{inner: NewMemoryControlStore()}
	service := newControlTestService(t, vectorStore, faults)
	faults.setLoadError(errors.New("runtime load unavailable"))
	if _, err := service.SearchDocuments(ctx, SearchDocumentsRequest{Query: "closed"}); !errors.Is(err, ErrControlStoreUnavailable) {
		t.Fatalf("runtime load error = %v, want ErrControlStoreUnavailable", err)
	}
	faults.setLoadError(nil)

	request := IndexDocumentVersionRequest{
		OperationID: "failed-save", DocumentID: "failed-save", VersionID: "v1",
		OwnerSpaceID: "owner", Filename: "failed.md", Content: []byte("orphanneedle"),
	}
	faults.setSaveError(errors.New("save unavailable"))
	if _, err := service.IndexDocumentVersion(ctx, request); !errors.Is(err, ErrControlStoreUnavailable) {
		t.Fatalf("save error = %v, want ErrControlStoreUnavailable", err)
	}
	faults.setSaveError(nil)
	observer := newControlTestService(t, vectorStore, faults)
	state, err := observer.GetDocumentVersionState(ctx, GetDocumentVersionStateRequest{DocumentID: request.DocumentID, VersionID: request.VersionID})
	if err != nil || state.Exists {
		t.Fatalf("failed save state exists=%v error=%v", state.Exists, err)
	}
	assertContractHitCount(t, observer, SearchDocumentsRequest{Query: "orphanneedle", AllowedSpaceIDs: []string{"owner"}}, 0)
	if _, err := observer.IndexDocumentVersion(ctx, request); err != nil {
		t.Fatalf("retry definite failure: %v", err)
	}

	ambiguousRequest := IndexDocumentVersionRequest{
		OperationID: "ambiguous-save", DocumentID: "ambiguous", VersionID: "v1",
		OwnerSpaceID: "owner", Filename: "ambiguous.md", Content: []byte("ambiguousneedle"),
	}
	faults.commitThenFail(errors.New("connection lost after commit"))
	if _, err := observer.IndexDocumentVersion(ctx, ambiguousRequest); !errors.Is(err, ErrControlStoreUnavailable) {
		t.Fatalf("ambiguous save error = %v, want ErrControlStoreUnavailable", err)
	}
	replayed, err := observer.IndexDocumentVersion(ctx, ambiguousRequest)
	if err != nil {
		t.Fatalf("ambiguous save replay: %v", err)
	}
	if replayed.DocumentID != ambiguousRequest.DocumentID || replayed.Status != DocumentVersionIndexed {
		t.Fatalf("ambiguous save replay = %+v", replayed)
	}
}

func TestLogicalDeletePrecedesRetryableVectorCleanup(t *testing.T) {
	ctx := context.Background()
	vectorStore := newDeleteFaultStore()
	controlStore := NewMemoryControlStore()
	service := newControlTestService(t, vectorStore, controlStore)
	mustIndexContractVersion(t, service, "cleanup", "v1", "owner", 1, "cleanupneedle")
	mustActivateContractVersion(t, service, "cleanup", "v1", 1, 1)
	if _, err := service.UpdateDocumentAccess(ctx, UpdateDocumentAccessRequest{
		OperationID: "cleanup-public", DocumentID: "cleanup", AccessRevision: 1,
		LifecycleRevision: 1, AuthenticatedPublic: true,
	}); err != nil {
		t.Fatal(err)
	}
	vectorStore.setDeleteError(errors.New("vector unavailable"))
	deleted, err := service.DeleteDocument(ctx, DeleteDocumentRequest{
		OperationID: "cleanup-delete", DocumentID: "cleanup", LifecycleRevision: 2,
	})
	if err != nil || !deleted.Tombstoned {
		t.Fatalf("logical delete = %+v error=%v", deleted, err)
	}
	state, err := controlStore.Load(ctx)
	if err != nil || len(state.PendingVectorDeletes) != 1 {
		t.Fatalf("pending cleanup count=%d error=%v", len(state.PendingVectorDeletes), err)
	}
	assertContractHitCount(t, service, SearchDocumentsRequest{Query: "cleanupneedle", TopK: 1}, 0)
	if len(vectorStore.inner.snapshot()) == 0 {
		t.Fatal("vector chunks were removed while delete failure was enabled")
	}

	vectorStore.setDeleteError(nil)
	restarted := newControlTestService(t, vectorStore, controlStore)
	assertContractHitCount(t, restarted, SearchDocumentsRequest{Query: "cleanupneedle", TopK: 1}, 0)
	state, err = controlStore.Load(ctx)
	if err != nil || len(state.PendingVectorDeletes) != 0 {
		t.Fatalf("pending cleanup after recovery=%d error=%v", len(state.PendingVectorDeletes), err)
	}
	if len(vectorStore.inner.snapshot()) != 0 {
		t.Fatalf("vector chunks after recovery = %d, want 0", len(vectorStore.inner.snapshot()))
	}
}

func TestDeleteDoesNotTouchVectorBeforeControlCommit(t *testing.T) {
	ctx := context.Background()
	vectorStore := newDeleteFaultStore()
	controlStore := &faultControlStore{inner: NewMemoryControlStore()}
	service := newControlTestService(t, vectorStore, controlStore)
	mustIndexContractVersion(t, service, "delete-order", "v1", "owner", 1, "deleteorderneedle")
	mustActivateContractVersion(t, service, "delete-order", "v1", 1, 1)
	controlStore.setSaveError(errors.New("control unavailable"))
	if _, err := service.DeleteDocument(ctx, DeleteDocumentRequest{
		OperationID: "delete-order", DocumentID: "delete-order", LifecycleRevision: 2,
	}); !errors.Is(err, ErrControlStoreUnavailable) {
		t.Fatalf("delete error = %v, want ErrControlStoreUnavailable", err)
	}
	if vectorStore.deleteCallCount() != 0 {
		t.Fatalf("vector delete calls = %d, want 0", vectorStore.deleteCallCount())
	}
}

func TestExpiredVectorWriteIntentIsCleanedAfterRestart(t *testing.T) {
	ctx := context.Background()
	vectorStore := NewMemoryStore()
	storageID := "expired-storage"
	if err := vectorStore.ReplaceDocument(ctx, storageID, []IndexedChunk{{
		Chunk: Chunk{ID: "expired-chunk", DocumentID: storageID, Content: "expired"},
	}}); err != nil {
		t.Fatal(err)
	}
	controlStore := NewMemoryControlStore()
	state, err := controlStore.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	state.PendingVectorWrites[storageID] = ControlPendingVectorWrite{
		OperationID: "expired-operation", Fingerprint: "expired-fingerprint", LeaseExpiresAtUnixMilli: 1,
	}
	if _, err := controlStore.Save(ctx, state.Generation, state); err != nil {
		t.Fatal(err)
	}
	service := newControlTestService(t, vectorStore, controlStore)
	if _, err := service.GetDocumentVersionState(ctx, GetDocumentVersionStateRequest{DocumentID: "missing", VersionID: "v1"}); err != nil {
		t.Fatal(err)
	}
	loaded, err := controlStore.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.PendingVectorWrites) != 0 || len(vectorStore.snapshot()) != 0 {
		t.Fatalf("expired intent count=%d vector chunks=%d", len(loaded.PendingVectorWrites), len(vectorStore.snapshot()))
	}
}

func TestControlStateRejectsCorruptSnapshot(t *testing.T) {
	state := newControlState()
	key := documentVersionKey("document", "v1")
	state.VersionsByKey[key] = ControlVersion{
		State:     DocumentVersionState{DocumentID: "document", VersionID: "v1", OwnerSpaceID: "owner", ContentSHA256: "digest"},
		StorageID: "storage",
	}
	state.VersionsByStore["storage"] = key
	state.Manifests["document"] = ControlDocumentManifest{OwnerSpaceID: "owner"}
	if err := validateControlState(state); err == nil {
		t.Fatal("missing immutable fingerprint should be rejected")
	}

	state = newControlState()
	state.Operations["invalid"] = ControlOperation{
		Kind:        operationDeleteDocument,
		Fingerprint: "fingerprint",
		Result: ControlOperationResult{
			DocumentVersion: &DocumentVersionState{DocumentID: "document", VersionID: "v1"},
		},
	}
	if err := validateControlState(state); err == nil {
		t.Fatal("mismatched operation result should be rejected")
	}

	tests := map[string]func(ControlState) ControlState{
		"mismatched version key": func(input ControlState) ControlState {
			version := input.VersionsByKey[documentVersionKey("document", "v1")]
			delete(input.VersionsByKey, documentVersionKey("document", "v1"))
			input.VersionsByKey["wrong"] = version
			input.VersionsByStore["storage"] = "wrong"
			return input
		},
		"fingerprint digest": func(input ControlState) ControlState {
			fingerprint := input.VersionFingerprints[documentVersionKey("document", "v1")]
			fingerprint.ContentSHA256 = "different"
			input.VersionFingerprints[documentVersionKey("document", "v1")] = fingerprint
			return input
		},
		"manifest owner": func(input ControlState) ControlState {
			manifest := input.Manifests["document"]
			manifest.OwnerSpaceID = "different"
			input.Manifests["document"] = manifest
			return input
		},
		"missing active version": func(input ControlState) ControlState {
			manifest := input.Manifests["document"]
			manifest.ActiveVersionID = "missing"
			input.Manifests["document"] = manifest
			return input
		},
		"tombstone with active version": func(input ControlState) ControlState {
			manifest := input.Manifests["document"]
			manifest.Tombstoned = true
			manifest.TombstoneRevision = 2
			manifest.LifecycleRevision = 2
			input.Manifests["document"] = manifest
			return input
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			if err := validateControlState(mutate(validControlStateForValidation())); err == nil {
				t.Fatal("unsafe snapshot should be rejected")
			}
		})
	}
}

func validControlStateForValidation() ControlState {
	state := newControlState()
	key := documentVersionKey("document", "v1")
	state.VersionsByKey[key] = ControlVersion{
		State: DocumentVersionState{
			DocumentID: "document", VersionID: "v1", OwnerSpaceID: "owner",
			Status: DocumentVersionActive, ContentSHA256: "digest",
		},
		StorageID: "storage",
	}
	state.VersionsByStore["storage"] = key
	state.VersionFingerprints[key] = ControlVersionFingerprint{ContentSHA256: "digest", OwnerSpaceID: "owner"}
	state.Manifests["document"] = ControlDocumentManifest{OwnerSpaceID: "owner", ActiveVersionID: "v1"}
	return state
}

func newControlTestService(t *testing.T, vectorStore VectorStore, controlStore ControlStore) *DocumentIndexService {
	t.Helper()
	core, err := NewServiceWithStore(context.Background(), vectorStore)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })
	service, err := NewDocumentIndexServiceWithControlStore(context.Background(), core, controlStore)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

type faultControlStore struct {
	mu              sync.Mutex
	inner           ControlStore
	loadErr         error
	saveErr         error
	commitErrorOnce error
}

func (s *faultControlStore) Load(ctx context.Context) (ControlState, error) {
	s.mu.Lock()
	err := s.loadErr
	s.mu.Unlock()
	if err != nil {
		return ControlState{}, err
	}
	return s.inner.Load(ctx)
}

func (s *faultControlStore) Save(ctx context.Context, generation uint64, state ControlState) (uint64, error) {
	s.mu.Lock()
	err := s.saveErr
	commitError := s.commitErrorOnce
	s.commitErrorOnce = nil
	s.mu.Unlock()
	if err != nil {
		return 0, err
	}
	next, err := s.inner.Save(ctx, generation, state)
	if err != nil {
		return 0, err
	}
	if commitError != nil {
		return 0, commitError
	}
	return next, nil
}

func (s *faultControlStore) Generation(ctx context.Context) (uint64, error) {
	s.mu.Lock()
	err := s.loadErr
	s.mu.Unlock()
	if err != nil {
		return 0, err
	}
	return s.inner.Generation(ctx)
}

func (s *faultControlStore) StorageDomain() string { return s.inner.StorageDomain() }

func (s *faultControlStore) setLoadError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loadErr = err
}

func (s *faultControlStore) setSaveError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saveErr = err
}

func (s *faultControlStore) commitThenFail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commitErrorOnce = err
}

type firstSavesBarrierControlStore struct {
	inner     ControlStore
	mu        sync.Mutex
	target    int
	remaining int
	entered   int
	release   chan struct{}
}

func newFirstSavesBarrierControlStore(inner ControlStore, count int) *firstSavesBarrierControlStore {
	return &firstSavesBarrierControlStore{inner: inner, target: count, remaining: count, release: make(chan struct{})}
}

func (s *firstSavesBarrierControlStore) Load(ctx context.Context) (ControlState, error) {
	return s.inner.Load(ctx)
}

func (s *firstSavesBarrierControlStore) Save(ctx context.Context, generation uint64, state ControlState) (uint64, error) {
	s.mu.Lock()
	wait := s.remaining > 0
	if wait {
		s.remaining--
		s.entered++
		if s.entered == s.target {
			close(s.release)
		}
	}
	release := s.release
	s.mu.Unlock()
	if wait {
		<-release
	}
	return s.inner.Save(ctx, generation, state)
}

func (s *firstSavesBarrierControlStore) Generation(ctx context.Context) (uint64, error) {
	return s.inner.Generation(ctx)
}

func (s *firstSavesBarrierControlStore) StorageDomain() string { return s.inner.StorageDomain() }

type deleteFaultStore struct {
	mu          sync.Mutex
	inner       *MemoryStore
	deleteErr   error
	deleteCalls int
}

type ingestCommitErrorStore struct {
	mu        sync.Mutex
	inner     *MemoryStore
	ingestErr error
}

func newIngestCommitErrorStore() *ingestCommitErrorStore {
	return &ingestCommitErrorStore{inner: NewMemoryStore()}
}

func (s *ingestCommitErrorStore) failNextIngest(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ingestErr = err
}

func (s *ingestCommitErrorStore) ReplaceDocument(ctx context.Context, documentID string, chunks []IndexedChunk) error {
	if err := s.inner.ReplaceDocument(ctx, documentID, chunks); err != nil {
		return err
	}
	if len(chunks) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.ingestErr
	s.ingestErr = nil
	return err
}

func (s *ingestCommitErrorStore) DenseSearch(ctx context.Context, query []float64, limit int) ([]ScoredChunk, error) {
	return s.inner.DenseSearch(ctx, query, limit)
}

func (s *ingestCommitErrorStore) SparseSearch(ctx context.Context, query []string, limit int) ([]ScoredChunk, error) {
	return s.inner.SparseSearch(ctx, query, limit)
}

func (s *ingestCommitErrorStore) Close() error { return nil }

func newDeleteFaultStore() *deleteFaultStore {
	return &deleteFaultStore{inner: NewMemoryStore()}
}

func (s *deleteFaultStore) setDeleteError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleteErr = err
}

func (s *deleteFaultStore) ReplaceDocument(ctx context.Context, documentID string, chunks []IndexedChunk) error {
	s.mu.Lock()
	err := s.deleteErr
	if len(chunks) == 0 {
		s.deleteCalls++
	}
	s.mu.Unlock()
	if len(chunks) == 0 && err != nil {
		return err
	}
	return s.inner.ReplaceDocument(ctx, documentID, chunks)
}

func (s *deleteFaultStore) deleteCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deleteCalls
}

func (s *deleteFaultStore) DenseSearch(ctx context.Context, query []float64, limit int) ([]ScoredChunk, error) {
	return s.inner.DenseSearch(ctx, query, limit)
}

func (s *deleteFaultStore) SparseSearch(ctx context.Context, query []string, limit int) ([]ScoredChunk, error) {
	return s.inner.SparseSearch(ctx, query, limit)
}

func (s *deleteFaultStore) Close() error { return nil }

var _ ControlStore = (*faultControlStore)(nil)
var _ ControlStore = (*firstSavesBarrierControlStore)(nil)
var _ VectorStore = (*deleteFaultStore)(nil)
var _ VectorStore = (*ingestCommitErrorStore)(nil)
