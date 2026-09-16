package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"gin-backend/internal/modules/document/domain"

	"github.com/google/uuid"
)

const (
	defaultPageSize      = 10
	maximumPageSize      = 100
	maximumSearchKeyword = 100
)

var (
	ErrQueryInvalidInput = errors.New("document query: invalid input")
	ErrQueryNotFound     = errors.New("document query: document not found")
	ErrQueryForbidden    = errors.New("document query: forbidden")
)

type Page struct {
	Number int
	Size   int
	Total  int64
}

type QueryService struct {
	repository domain.QueryRepository
	cache      domain.QueryCache
	shadow     ShadowSearchScheduler
}

type QueryOption func(*QueryService)

func WithShadowSearchScheduler(shadow ShadowSearchScheduler) QueryOption {
	return func(service *QueryService) {
		service.shadow = shadow
	}
}

func NewQueryService(repository domain.QueryRepository, cache domain.QueryCache, options ...QueryOption) (*QueryService, error) {
	if repository == nil {
		return nil, errors.New("document query: repository is nil")
	}
	service := &QueryService{repository: repository, cache: cache}
	for _, option := range options {
		if option != nil {
			option(service)
		}
	}
	return service, nil
}

func (service *QueryService) Get(ctx context.Context, viewerID int64, documentID string) (*domain.DocumentView, error) {
	if viewerID <= 0 {
		return nil, fmt.Errorf("%w: viewer id must be positive", ErrQueryInvalidInput)
	}
	documentID, err := normalizeDocumentID(documentID)
	if err != nil {
		return nil, err
	}

	head, err := service.repository.GetActiveDocumentHead(ctx, documentID)
	if err != nil {
		return nil, translateQueryRepositoryError("get document head", err)
	}
	if head.OwnerID != viewerID && !head.AuthenticatedPublic {
		return nil, ErrQueryForbidden
	}

	loader := func(loadContext context.Context) (*domain.DocumentView, error) {
		view, loadErr := service.repository.GetActiveDocumentView(loadContext, *head)
		if loadErr != nil {
			return nil, translateQueryRepositoryError("get document view", loadErr)
		}
		return view, nil
	}
	if service.cache == nil {
		return loader(ctx)
	}
	view, err := service.cache.GetDocumentView(ctx, *head, loader)
	if err != nil {
		return nil, err
	}
	return view, nil
}

func (service *QueryService) ListMine(ctx context.Context, ownerID int64, page, pageSize int) ([]domain.DocumentSummary, Page, error) {
	if ownerID <= 0 {
		return nil, Page{}, fmt.Errorf("%w: owner id must be positive", ErrQueryInvalidInput)
	}
	page, pageSize = normalizePage(page, pageSize)
	items, total, err := service.repository.ListOwnedDocuments(ctx, ownerID, (page-1)*pageSize, pageSize)
	if err != nil {
		return nil, Page{}, fmt.Errorf("list owned documents: %w", err)
	}
	return items, Page{Number: page, Size: pageSize, Total: total}, nil
}

func (service *QueryService) ListPublic(ctx context.Context, page, pageSize int) ([]domain.DocumentSummary, Page, error) {
	page, pageSize = normalizePage(page, pageSize)
	items, total, err := service.repository.ListPublicDocuments(ctx, (page-1)*pageSize, pageSize)
	if err != nil {
		return nil, Page{}, fmt.Errorf("list public documents: %w", err)
	}
	return items, Page{Number: page, Size: pageSize, Total: total}, nil
}

func (service *QueryService) SearchMine(ctx context.Context, ownerID int64, keyword string, page, pageSize int) ([]domain.DocumentSummary, Page, string, error) {
	if ownerID <= 0 {
		return nil, Page{}, "", fmt.Errorf("%w: owner id must be positive", ErrQueryInvalidInput)
	}
	keyword = strings.TrimSpace(keyword)
	if keyword == "" || utf8.RuneCountInString(keyword) > maximumSearchKeyword {
		return nil, Page{}, "", fmt.Errorf("%w: keyword must contain 1 to %d characters", ErrQueryInvalidInput, maximumSearchKeyword)
	}
	page, pageSize = normalizePage(page, pageSize)
	startedAt := time.Now()
	items, total, err := service.repository.SearchOwnedDocuments(ctx, ownerID, keyword, (page-1)*pageSize, pageSize)
	if err != nil {
		return nil, Page{}, "", fmt.Errorf("search owned documents: %w", err)
	}
	if service.shadow != nil {
		documentIDs := make([]string, 0, len(items))
		for _, item := range items {
			documentIDs = append(documentIDs, item.DocumentID)
		}
		service.shadow.Observe(ShadowSearchRequest{
			Source: "runtime", OwnerID: ownerID, Query: keyword, Page: page, PageSize: pageSize,
			BM25Total: total, BM25Latency: time.Since(startedAt), BM25DocumentIDs: documentIDs,
		})
	}
	return items, Page{Number: page, Size: pageSize, Total: total}, keyword, nil
}

func normalizePage(page, pageSize int) (int, int) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > maximumPageSize {
		pageSize = defaultPageSize
	}
	return page, pageSize
}

func normalizeDocumentID(value string) (string, error) {
	parsed, err := uuid.Parse(strings.TrimSpace(value))
	if err != nil {
		return "", fmt.Errorf("%w: document id must be a UUID", ErrQueryInvalidInput)
	}
	return parsed.String(), nil
}

func translateQueryRepositoryError(operation string, err error) error {
	if errors.Is(err, domain.ErrNotFound) {
		return fmt.Errorf("%s: %w", operation, ErrQueryNotFound)
	}
	return fmt.Errorf("%s: %w", operation, err)
}
