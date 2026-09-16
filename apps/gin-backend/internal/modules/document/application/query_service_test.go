package application

import (
	"context"
	"errors"
	"testing"

	"gin-backend/internal/modules/document/domain"
)

const queryTestDocumentID = "00000000-0000-0000-0000-000000000101"

type queryRepositoryStub struct {
	head      *domain.DocumentHead
	view      *domain.DocumentView
	err       error
	viewCalls int
	offset    int
	limit     int
	keyword   string
	summaries []domain.DocumentSummary
	total     int64
}

func (stub *queryRepositoryStub) GetActiveDocumentHead(context.Context, string) (*domain.DocumentHead, error) {
	return stub.head, stub.err
}

func (stub *queryRepositoryStub) GetActiveDocumentView(context.Context, domain.DocumentHead) (*domain.DocumentView, error) {
	stub.viewCalls++
	return stub.view, stub.err
}

func (stub *queryRepositoryStub) ListOwnedDocuments(_ context.Context, _ int64, offset, limit int) ([]domain.DocumentSummary, int64, error) {
	stub.offset, stub.limit = offset, limit
	return stub.summaries, stub.total, stub.err
}

func (stub *queryRepositoryStub) ListPublicDocuments(_ context.Context, offset, limit int) ([]domain.DocumentSummary, int64, error) {
	stub.offset, stub.limit = offset, limit
	return stub.summaries, stub.total, stub.err
}

func (stub *queryRepositoryStub) SearchOwnedDocuments(_ context.Context, _ int64, keyword string, offset, limit int) ([]domain.DocumentSummary, int64, error) {
	stub.keyword, stub.offset, stub.limit = keyword, offset, limit
	return stub.summaries, stub.total, stub.err
}

type queryCacheStub struct {
	head  domain.DocumentHead
	calls int
}

type shadowSchedulerStub struct {
	request ShadowSearchRequest
	calls   int
}

func (stub *shadowSchedulerStub) Observe(request ShadowSearchRequest) bool {
	stub.request, stub.calls = request, stub.calls+1
	return true
}

func (stub *queryCacheStub) GetDocumentView(ctx context.Context, head domain.DocumentHead, loader domain.DocumentViewLoader) (*domain.DocumentView, error) {
	stub.head, stub.calls = head, stub.calls+1
	return loader(ctx)
}

func TestQueryServiceRejectsPrivateDocumentBeforeLoadingContent(t *testing.T) {
	repository := &queryRepositoryStub{head: &domain.DocumentHead{
		DocumentID: queryTestDocumentID, OwnerID: 1, ActiveVersionID: "version", AuthenticatedPublic: false,
	}}
	service, err := NewQueryService(repository, &queryCacheStub{})
	if err != nil {
		t.Fatal(err)
	}

	_, err = service.Get(context.Background(), 2, queryTestDocumentID)
	if !errors.Is(err, ErrQueryForbidden) {
		t.Fatalf("Get() error = %v, want ErrQueryForbidden", err)
	}
	if repository.viewCalls != 0 {
		t.Fatalf("private content was loaded before authorization: calls=%d", repository.viewCalls)
	}
}

func TestQueryServiceUsesAuthorizedHeadAsCacheIdentity(t *testing.T) {
	head := &domain.DocumentHead{
		DocumentID: queryTestDocumentID, OwnerID: 1, ActiveVersionID: "version",
		ActivationRevision: 2, AccessRevision: 3, LifecycleRevision: 4,
	}
	view := &domain.DocumentView{DocumentHead: *head, Title: "title", Content: "content"}
	repository := &queryRepositoryStub{head: head, view: view}
	cache := &queryCacheStub{}
	service, _ := NewQueryService(repository, cache)

	got, err := service.Get(context.Background(), 1, queryTestDocumentID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "content" || cache.calls != 1 || cache.head.AccessRevision != 3 {
		t.Fatalf("unexpected cached result: got=%#v cache=%#v", got, cache)
	}
}

func TestQueryServiceNormalizesSearchAndPagination(t *testing.T) {
	repository := &queryRepositoryStub{total: 11, summaries: []domain.DocumentSummary{{DocumentID: queryTestDocumentID}}}
	shadow := &shadowSchedulerStub{}
	service, _ := NewQueryService(repository, nil, WithShadowSearchScheduler(shadow))

	_, page, keyword, err := service.SearchMine(context.Background(), 1, "  Go 搜索  ", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if keyword != "Go 搜索" || repository.keyword != keyword {
		t.Fatalf("keyword = %q repository keyword = %q", keyword, repository.keyword)
	}
	if page.Number != 1 || page.Size != 10 || page.Total != 11 || repository.offset != 0 || repository.limit != 10 {
		t.Fatalf("unexpected pagination: page=%#v offset=%d limit=%d", page, repository.offset, repository.limit)
	}
	if shadow.calls != 1 || shadow.request.Query != keyword || shadow.request.OwnerID != 1 ||
		len(shadow.request.BM25DocumentIDs) != 1 || shadow.request.BM25DocumentIDs[0] != queryTestDocumentID {
		t.Fatalf("shadow request = %#v calls=%d", shadow.request, shadow.calls)
	}
}

func TestQueryServiceMapsRepositoryNotFound(t *testing.T) {
	repository := &queryRepositoryStub{err: domain.ErrNotFound}
	service, _ := NewQueryService(repository, nil)

	_, err := service.Get(context.Background(), 1, queryTestDocumentID)
	if !errors.Is(err, ErrQueryNotFound) {
		t.Fatalf("Get() error = %v, want ErrQueryNotFound", err)
	}
}
