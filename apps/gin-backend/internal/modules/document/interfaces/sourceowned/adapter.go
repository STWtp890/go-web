// Package sourceowned serves the Web Documents surface on top of the source-owned
// services.
//
// After ADR-017 the formal document, its versions, spaces, membership and
// resource permission belong to the document service. Writes and detail reads in
// this package are therefore business commands and detail queries against that
// service, and a search presents a resource capability the document service
// minted to the formal document search service. Nothing here touches a document
// business table.
//
// The HTTP contract, request shapes and response envelopes stay exactly as the
// Web front end already consumes them: this package only changes where the data
// comes from. It lives under interfaces/ rather than infrastructure/ because it
// is a transport adapter over remote service clients, not a persistence adapter
// for a domain port.
package sourceowned

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	baseerrors "gin-backend/internal/common/base/errors"
	"gin-backend/internal/common/base/responses"
	"gin-backend/internal/modules/document/domain"
	"gin-backend/internal/platform/httpserver/identity"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// defaultListPageSize 是列表未指定每页数量时的页大小。
const defaultListPageSize = 10

// DocumentGateway is the business boundary the Web surface calls. It is declared
// here, next to its only caller, so the HTTP adapter stays free of client and
// transport details and is testable without a live service.
type DocumentGateway interface {
	Create(ctx context.Context, command CreateCommand) (*MutationResult, error)
	Update(ctx context.Context, command UpdateCommand) (*MutationResult, error)
	Trash(ctx context.Context, command TrashCommand) error
	Get(ctx context.Context, viewerID int64, documentID string) (*domain.DocumentView, error)
	ListOwned(ctx context.Context, ownerID int64, pageSize int, cursor string) ([]domain.DocumentSummary, domain.Page, error)
	ListPublic(ctx context.Context, viewerID int64, pageSize int, cursor string) ([]domain.DocumentSummary, domain.Page, error)
	Search(ctx context.Context, viewerID int64, keyword string, page, pageSize int) ([]domain.DocumentSummary, domain.Page, string, error)
}

// ServiceAdapter serves the Web Documents surface on top of the source-owned
// services. The composition root registers it only when those services are
// configured; without them the surface is absent, not degraded to table access.
type ServiceAdapter struct {
	gateway DocumentGateway
}

// NewServiceAdapter validates the wiring.
func NewServiceAdapter(gateway DocumentGateway) (*ServiceAdapter, error) {
	if gateway == nil {
		return nil, ErrNotConfigured
	}
	return &ServiceAdapter{gateway: gateway}, nil
}

// RegisterRoutes mounts the Web Documents surface.
func (adapter *ServiceAdapter) RegisterRoutes(_ *gin.RouterGroup, protected *gin.RouterGroup) {
	documents := protected.Group("documents")
	documents.POST("", adapter.create)
	documents.GET("/mine", adapter.listMine)
	documents.GET("/public", adapter.listPublic)
	documents.GET("/search", adapter.searchMine)
	documents.GET("/:documentId", adapter.get)
	documents.PUT("/:documentId", adapter.update)
	documents.DELETE("/:documentId", adapter.trash)
}

type mutationRequest struct {
	Title      string `json:"title" binding:"required,min=1,max=255"`
	Content    string `json:"content" binding:"required"`
	Visibility string `json:"visibility" binding:"omitempty,oneof=public private"`
	// ExpectedRevision is the optional optimistic-concurrency guard the editor
	// sends when it knows which revision it started from. 0 means "do not check".
	ExpectedRevision uint64 `json:"expectedRevision"`
	// RequestID is the optional idempotency key. A retried save that reuses the
	// key replays the first attempt instead of appending a second version; when
	// absent the surface mints one per request.
	RequestID string `json:"requestId" binding:"omitempty,max=128"`
}

// listQuery is the cursor-paginated list query.
//
// cursor is opaque: it is echoed back to the document service exactly as issued.
// page is only the ordinal the client is displaying; it never selects rows, so a
// stale page number cannot silently shift the result window.
type listQuery struct {
	Page     int    `form:"page" binding:"omitempty,min=1"`
	PageSize int    `form:"pageSize" binding:"omitempty,min=1,max=100"`
	Cursor   string `form:"cursor"`
}

// searchQuery keeps the page-numbered shape the Web search surface already
// consumes: the search response reports a real total, so page arithmetic is
// meaningful there.
type searchQuery struct {
	Keyword  string `form:"keyword" binding:"required"`
	Page     int    `form:"page" binding:"omitempty,min=1"`
	PageSize int    `form:"pageSize" binding:"omitempty,min=1,max=100"`
}

type documentSummaryResponse struct {
	DocumentID string `json:"documentId"`
	OwnerID    string `json:"ownerId"`
	Title      string `json:"title"`
	Summary    string `json:"summary"`
	Visibility string `json:"visibility"`
	CreatedAt  int64  `json:"createdAt"`
	UpdatedAt  int64  `json:"updatedAt"`
}

func (adapter *ServiceAdapter) create(c *gin.Context) {
	ownerID, ok := identity.UserID(c)
	if !ok {
		writeUnauthorized(c)
		return
	}
	request := new(mutationRequest)
	if err := c.ShouldBindJSON(request); err != nil {
		responses.Fail(c, http.StatusBadRequest, baseerrors.CodeValidationFailed, "请检查输入参数")
		return
	}
	result, err := adapter.gateway.Create(c.Request.Context(), CreateCommand{
		OwnerID: ownerID, Title: request.Title, Content: request.Content,
		AuthenticatedPublic: request.Visibility == "public",
		RequestID:           requestID(request.RequestID),
	})
	if err != nil {
		writeGatewayError(c, err, "创建文章失败")
		return
	}
	view := MutationDetailToView(result.Document)
	responses.Created(c, gin.H{
		"documentId": view.DocumentID,
		"title":      view.Title, "summary": view.Summary,
		"visibility": visibility(view.AuthenticatedPublic),
		"createdAt":  unix(view.CreatedAt), "updatedAt": unix(view.UpdatedAt),
		"revision": revision(view.ActivationRevision),
	})
}

func (adapter *ServiceAdapter) update(c *gin.Context) {
	ownerID, ok := identity.UserID(c)
	if !ok {
		writeUnauthorized(c)
		return
	}
	request := new(mutationRequest)
	if err := c.ShouldBindJSON(request); err != nil || request.Visibility == "" {
		responses.Fail(c, http.StatusBadRequest, baseerrors.CodeValidationFailed, "请检查输入参数")
		return
	}
	result, err := adapter.gateway.Update(c.Request.Context(), UpdateCommand{
		OwnerID: ownerID, DocumentID: c.Param("documentId"),
		Title: request.Title, Content: request.Content,
		// The editor always states a visibility, so the policy change is sent
		// explicitly: the document service applies it in the same transaction as
		// the new version, which is what keeps a list read and a detail read of
		// the same document from disagreeing about visibility.
		AuthenticatedPublic:       request.Visibility == "public",
		ExpectedAggregateRevision: request.ExpectedRevision,
		RequestID:                 requestID(request.RequestID),
	})
	if err != nil {
		writeGatewayError(c, err, "保存文章失败")
		return
	}
	view := MutationDetailToView(result.Document)
	responses.OK(c, gin.H{
		"documentId": view.DocumentID,
		"title":      view.Title, "summary": view.Summary,
		"visibility": visibility(view.AuthenticatedPublic), "content": view.Content,
		"createdAt": unix(view.CreatedAt), "updatedAt": unix(view.UpdatedAt),
		"revision": revision(view.ActivationRevision), "replayed": result.Replayed,
		// The version this request id produced: on a replay it is the version the
		// first attempt wrote, which lets the editor tell "already applied" from
		// "just applied".
		"appliedVersionId": result.AppliedVersionID,
	})
}

func (adapter *ServiceAdapter) trash(c *gin.Context) {
	ownerID, ok := identity.UserID(c)
	if !ok {
		writeUnauthorized(c)
		return
	}
	documentID := c.Param("documentId")
	if err := adapter.gateway.Trash(c.Request.Context(), TrashCommand{OwnerID: ownerID, DocumentID: documentID}); err != nil {
		writeGatewayError(c, err, "删除文章失败")
		return
	}
	responses.OK(c, gin.H{"documentId": documentID})
}

func (adapter *ServiceAdapter) get(c *gin.Context) {
	viewerID, ok := identity.UserID(c)
	if !ok {
		writeUnauthorized(c)
		return
	}
	view, err := adapter.gateway.Get(c.Request.Context(), viewerID, c.Param("documentId"))
	if err != nil {
		writeGatewayError(c, err, "获取文章失败")
		return
	}
	owner := ""
	if view.OwnerID > 0 {
		owner = strconv.FormatInt(view.OwnerID, 10)
	}
	responses.OK(c, gin.H{
		"documentId": view.DocumentID, "ownerId": owner,
		"title": view.Title, "summary": view.Summary,
		"visibility": visibility(view.AuthenticatedPublic), "content": view.Content,
		"createdAt": unix(view.CreatedAt), "updatedAt": unix(view.UpdatedAt),
		// The revision the editor started from: sending it back makes the save an
		// optimistic-concurrency update instead of a blind overwrite.
		"revision": revision(view.ActivationRevision),
	})
}

// revision renders the aggregate revision as a number the editor can send back.
// It is the concurrency token, not a display field.
func revision(value int64) uint64 {
	if value <= 0 {
		return 0
	}
	return uint64(value)
}

func (adapter *ServiceAdapter) listMine(c *gin.Context) {
	ownerID, ok := identity.UserID(c)
	if !ok {
		writeUnauthorized(c)
		return
	}
	query := new(listQuery)
	if err := c.ShouldBindQuery(query); err != nil {
		responses.Fail(c, http.StatusBadRequest, baseerrors.CodeValidationFailed, "请检查分页参数")
		return
	}
	items, page, err := adapter.gateway.ListOwned(c.Request.Context(), ownerID, pageSize(query.PageSize), query.Cursor)
	if err != nil {
		writeGatewayError(c, err, "获取文章列表失败")
		return
	}
	writeList(c, items, page, query.Page, "")
}

func (adapter *ServiceAdapter) listPublic(c *gin.Context) {
	viewerID, ok := identity.UserID(c)
	if !ok {
		writeUnauthorized(c)
		return
	}
	query := new(listQuery)
	if err := c.ShouldBindQuery(query); err != nil {
		responses.Fail(c, http.StatusBadRequest, baseerrors.CodeValidationFailed, "请检查分页参数")
		return
	}
	items, page, err := adapter.gateway.ListPublic(c.Request.Context(), viewerID, pageSize(query.PageSize), query.Cursor)
	if err != nil {
		writeGatewayError(c, err, "获取公开文章失败")
		return
	}
	writeList(c, items, page, query.Page, "")
}

func (adapter *ServiceAdapter) searchMine(c *gin.Context) {
	viewerID, ok := identity.UserID(c)
	if !ok {
		writeUnauthorized(c)
		return
	}
	query := new(searchQuery)
	if err := c.ShouldBindQuery(query); err != nil {
		responses.Fail(c, http.StatusBadRequest, baseerrors.CodeValidationFailed, "请检查搜索参数")
		return
	}
	items, page, keyword, err := adapter.gateway.Search(c.Request.Context(), viewerID, query.Keyword, query.Page, pageSize(query.PageSize))
	if err != nil {
		slog.Error("document_search_failed", slog.Int64("viewer_id", viewerID), slog.String("error", err.Error()))
		writeGatewayError(c, err, "搜索文稿失败，请稍后重试")
		return
	}
	writeList(c, items, page, query.Page, keyword)
}

// writeList renders the list envelope the front end consumes.
//
// displayPage is only the ordinal the client says it is showing: the rows came
// from the cursor (or, for search, from the page number the search service
// honoured), so the meta reports the real total and hands the next cursor back
// untouched. The client keeps its own cursor history, which is what makes
// "previous page" possible without the server inventing a backward cursor.
//
// total_pages is computed from that real total, which is what keeps a clickable
// page from leading to an empty result: the last page the client can reach is
// exactly the last page the service can answer. truncated is forwarded for the
// same reason — the client has to be able to say "you are not seeing everything"
// instead of silently showing a page as if it were the whole answer.
func writeList(c *gin.Context, items []domain.DocumentSummary, page domain.Page, displayPage int, keyword string) {
	list := make([]documentSummaryResponse, 0, len(items))
	for _, item := range items {
		owner := ""
		if item.OwnerID > 0 {
			owner = strconv.FormatInt(item.OwnerID, 10)
		}
		list = append(list, documentSummaryResponse{
			DocumentID: item.DocumentID, OwnerID: owner,
			Title: item.Title, Summary: item.Summary, Visibility: visibility(item.AuthenticatedPublic),
			CreatedAt: unix(item.CreatedAt), UpdatedAt: unix(item.UpdatedAt),
		})
	}
	data := gin.H{"documentList": list}
	if keyword != "" {
		data["keyword"] = keyword
	}
	meta := responses.NewCursorPageMeta(displayPage, page.Size, page.Total, page.NextCursor)
	meta.Truncated = page.Truncated
	responses.OKWithMeta(c, data, meta)
}

// pageSize applies the surface's default when the client did not ask for one.
func pageSize(requested int) int {
	if requested <= 0 {
		return defaultListPageSize
	}
	return requested
}

// requestID returns the caller's idempotency key, or mints one so every save
// carries a stable key the document service can deduplicate on. A retried HTTP
// request that reuses the key replays instead of appending another version.
func requestID(requested string) string {
	requested = strings.TrimSpace(requested)
	if requested != "" {
		return requested
	}
	return uuid.NewString()
}

// writeGatewayError maps the stable gateway error classes to the HTTP status
// codes the Web front end already understands. Which class applies is decided by
// the document service, so go-web never guesses on the caller's behalf.
func writeGatewayError(c *gin.Context, err error, fallback string) {
	switch ClassifyError(err) {
	case StatusNotFound:
		responses.Fail(c, http.StatusNotFound, baseerrors.CodeNotFound, "文章不存在")
	case StatusForbidden:
		responses.Fail(c, http.StatusForbidden, baseerrors.CodeForbidden, "无权访问该文章")
	case StatusInvalidInput:
		responses.Fail(c, http.StatusBadRequest, baseerrors.CodeValidationFailed, "请检查输入参数")
	case StatusConflict:
		responses.Fail(c, http.StatusConflict, baseerrors.CodeConflict, "文章已被修改，请刷新后重试")
	case StatusUnavailable:
		slog.Error("document_gateway_unavailable", slog.String("error", err.Error()))
		responses.Fail(c, http.StatusServiceUnavailable, baseerrors.CodeServiceUnavail, fallback)
	default:
		slog.Error("document_gateway_failed", slog.String("error", err.Error()))
		responses.Fail(c, http.StatusInternalServerError, baseerrors.CodeInternalError, fallback)
	}
}

func writeUnauthorized(c *gin.Context) {
	responses.Fail(c, http.StatusUnauthorized, baseerrors.CodeUnauthorized, "请先登录")
}

func visibility(public bool) string {
	if public {
		return "public"
	}
	return "private"
}

// unix renders a service timestamp as epoch seconds. A zero time means the source
// did not carry one; it is reported as 0 so the Web layer renders "no date"
// instead of a fabricated instant near 1970.
func unix(value time.Time) int64 {
	if value.IsZero() {
		return 0
	}
	return value.Unix()
}
