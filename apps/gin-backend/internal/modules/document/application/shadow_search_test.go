package application

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"gin-backend/internal/modules/document/domain"
)

func TestShadowSearchObserverRecordsIndependentCorrectnessDimensions(t *testing.T) {
	store := &shadowStoreStub{
		spaceID: "space-owner", recorded: make(chan domain.ShadowSearchObservation, 1),
		facts: map[string]domain.ShadowSearchFact{
			"owner":         {DocumentID: "owner", OwnerID: 7, OwnerSpaceID: "space-owner", ActiveVersionID: "v1", LifecycleStatus: domain.LifecycleActive},
			"public-other":  {DocumentID: "public-other", OwnerID: 8, OwnerSpaceID: "space-other", ActiveVersionID: "v2", LifecycleStatus: domain.LifecycleActive, AuthenticatedPublic: true},
			"private-other": {DocumentID: "private-other", OwnerID: 8, OwnerSpaceID: "space-other", ActiveVersionID: "v3", LifecycleStatus: domain.LifecycleActive},
			"wrong-version": {DocumentID: "wrong-version", OwnerID: 7, OwnerSpaceID: "space-owner", ActiveVersionID: "v5", LifecycleStatus: domain.LifecycleActive},
		},
	}
	client := &shadowClientStub{result: domain.DocumentSearchResult{Hits: []domain.DocumentSearchHit{
		{DocumentID: "owner", VersionID: "v1"},
		{DocumentID: "owner", VersionID: "v1"},
		{DocumentID: "public-other", VersionID: "v2"},
		{DocumentID: "private-other", VersionID: "v3"},
		{DocumentID: "wrong-version", VersionID: "v4"},
		{DocumentID: "missing", VersionID: "v6"},
	}}}
	observer, err := NewShadowSearchObserver(store, client, ShadowSearchObserverConfig{
		QueueSize: 4, Concurrency: 1, Timeout: time.Second, RecordTimeout: time.Second, TopK: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer observer.Close()
	if !observer.Observe(ShadowSearchRequest{
		Source: "evaluation", OwnerID: 7, Query: "query", Page: 1, PageSize: 10,
		BM25DocumentIDs: []string{"owner"},
	}) {
		t.Fatal("observation was unexpectedly dropped")
	}
	select {
	case observation := <-store.recorded:
		if observation.Status != domain.ShadowSearchSucceeded || observation.OverlapCount != 1 {
			t.Fatalf("status/overlap = %s/%d", observation.Status, observation.OverlapCount)
		}
		if observation.PermissionViolationCount != 1 || observation.LifecycleViolationCount != 1 ||
			observation.ActiveVersionViolationCount != 1 || observation.FormalScopeMismatchCount != 1 {
			t.Fatalf("correctness counts = %#v", observation)
		}
		if len(observation.ComparableShadowDocumentIDs) != 1 || observation.ComparableShadowDocumentIDs[0] != "owner" {
			t.Fatalf("comparable ids = %#v", observation.ComparableShadowDocumentIDs)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for observation")
	}
}

func TestShadowSearchObserverDropsWhenBoundedQueueIsFull(t *testing.T) {
	store := &shadowStoreStub{spaceID: "space-owner", recorded: make(chan domain.ShadowSearchObservation, 4)}
	client := &shadowClientStub{block: make(chan struct{}), started: make(chan struct{}, 1)}
	observer, err := NewShadowSearchObserver(store, client, ShadowSearchObserverConfig{
		QueueSize: 1, Concurrency: 1, Timeout: time.Second, RecordTimeout: time.Second, TopK: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := ShadowSearchRequest{OwnerID: 7, Query: "query", Page: 1, PageSize: 10}
	if !observer.Observe(request) {
		t.Fatal("first observation was dropped")
	}
	select {
	case <-client.started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	if !observer.Observe(request) {
		t.Fatal("queued observation was dropped")
	}
	startedAt := time.Now()
	if observer.Observe(request) {
		t.Fatal("observation was accepted despite full queue")
	}
	if time.Since(startedAt) > 50*time.Millisecond {
		t.Fatal("full queue submission blocked the caller")
	}
	close(client.block)
	observer.Close()
}

func TestShadowSearchObserverDoesNotPersistQueryFromRemoteError(t *testing.T) {
	const query = "private query text"
	store := &shadowStoreStub{spaceID: "space-owner", recorded: make(chan domain.ShadowSearchObservation, 1)}
	client := &shadowClientStub{err: &domain.RemoteIndexError{
		Code: "InvalidArgument", Err: errors.New("invalid query: " + query),
	}}
	observer, err := NewShadowSearchObserver(store, client, ShadowSearchObserverConfig{
		QueueSize: 1, Concurrency: 1, Timeout: time.Second, RecordTimeout: time.Second, TopK: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer observer.Close()
	if !observer.Observe(ShadowSearchRequest{OwnerID: 7, Query: query, Page: 1, PageSize: 10}) {
		t.Fatal("observation was unexpectedly dropped")
	}
	select {
	case observation := <-store.recorded:
		if observation.ErrorMessage != "mixin-search InvalidArgument" {
			t.Fatalf("error message = %q", observation.ErrorMessage)
		}
		if strings.Contains(observation.ErrorMessage, query) {
			t.Fatalf("query leaked into persisted error message: %q", observation.ErrorMessage)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for observation")
	}
}

type shadowStoreStub struct {
	spaceID  string
	facts    map[string]domain.ShadowSearchFact
	recorded chan domain.ShadowSearchObservation
}

func (stub *shadowStoreStub) GetOwnerPrivateSpaceID(context.Context, int64) (string, error) {
	return stub.spaceID, nil
}

func (stub *shadowStoreStub) GetShadowSearchFacts(context.Context, []string) (map[string]domain.ShadowSearchFact, error) {
	return stub.facts, nil
}

func (stub *shadowStoreStub) RecordShadowSearchObservation(_ context.Context, observation domain.ShadowSearchObservation) error {
	stub.recorded <- observation
	return nil
}

func (stub *shadowStoreStub) GetShadowSearchStats(context.Context, string) (domain.ShadowSearchStats, error) {
	return domain.ShadowSearchStats{}, nil
}

type shadowClientStub struct {
	result  domain.DocumentSearchResult
	err     error
	block   chan struct{}
	started chan struct{}
}

func (stub *shadowClientStub) SearchDocuments(ctx context.Context, _ domain.DocumentSearchInput) (domain.DocumentSearchResult, error) {
	if stub.started != nil {
		select {
		case stub.started <- struct{}{}:
		default:
		}
	}
	if stub.block != nil {
		select {
		case <-stub.block:
		case <-ctx.Done():
			return domain.DocumentSearchResult{}, ctx.Err()
		}
	}
	return stub.result, stub.err
}
