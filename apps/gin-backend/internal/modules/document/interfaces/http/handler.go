// Package httpadapter 暴露 document 领域的 HTTP 接口。
package httpadapter

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	baseerrors "gin-backend/internal/common/base/errors"
	"gin-backend/internal/common/base/responses"
	"gin-backend/internal/modules/document/application"
	"gin-backend/internal/modules/document/domain"
	"gin-backend/internal/platform/httpserver/identity"

	"github.com/gin-gonic/gin"
)

type commandService interface {
	Create(context.Context, application.CreateCommand) (*application.MutationResult, error)
	Update(context.Context, application.UpdateCommand) (*application.MutationResult, error)
	Trash(context.Context, application.TrashCommand) error
}

type queryService interface {
	Get(context.Context, int64, string) (*domain.DocumentView, error)
	ListMine(context.Context, int64, int, int) ([]domain.DocumentSummary, application.Page, error)
	ListPublic(context.Context, int, int) ([]domain.DocumentSummary, application.Page, error)
	SearchMine(context.Context, int64, string, int, int) ([]domain.DocumentSummary, application.Page, string, error)
}

type Handler struct {
	commands commandService
	queries  queryService
}

func New(commands commandService, queries queryService) (*Handler, error) {
	if commands == nil || queries == nil {
		return nil, errors.New("document http: command and query services are required")
	}
	return &Handler{commands: commands, queries: queries}, nil
}

// RegisterRoutes 将 document 用例挂载到统一的 documents 路径。
func (handler *Handler) RegisterRoutes(_ *gin.RouterGroup, protected *gin.RouterGroup) {
	documents := protected.Group("documents")
	documents.POST("", handler.create)
	documents.GET("/mine", handler.listMine)
	documents.GET("/public", handler.listPublic)
	documents.GET("/search", handler.searchMine)
	documents.GET("/:documentId", handler.get)
	documents.PUT("/:documentId", handler.update)
	documents.DELETE("/:documentId", handler.trash)
}

type mutationRequest struct {
	Title      string `json:"title" binding:"required,min=1,max=255"`
	Content    string `json:"content" binding:"required"`
	Visibility string `json:"visibility" binding:"omitempty,oneof=public private"`
}

type pageQuery struct {
	Page     int `form:"page" binding:"omitempty,min=1"`
	PageSize int `form:"pageSize" binding:"omitempty,min=1,max=100"`
}

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

func (handler *Handler) create(c *gin.Context) {
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
	result, err := handler.commands.Create(c.Request.Context(), application.CreateCommand{
		OwnerID: ownerID, Title: request.Title, Content: request.Content,
		AuthenticatedPublic: request.Visibility == "public",
	})
	if err != nil {
		writeApplicationError(c, err, "创建文章失败")
		return
	}
	responses.Created(c, gin.H{
		"documentId": result.Document.DocumentID,
		"title":      result.Version.Title, "summary": result.Version.Summary,
		"visibility": visibility(result.Policy.AuthenticatedPublic),
		"createdAt":  result.Document.CreatedAt.Unix(), "updatedAt": result.Document.UpdatedAt.Unix(),
	})
}

func (handler *Handler) update(c *gin.Context) {
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
	result, err := handler.commands.Update(c.Request.Context(), application.UpdateCommand{
		OwnerID: ownerID, DocumentID: c.Param("documentId"),
		Title: request.Title, Content: request.Content,
		AuthenticatedPublic: request.Visibility == "public",
	})
	if err != nil {
		writeApplicationError(c, err, "保存文章失败")
		return
	}
	responses.OK(c, gin.H{
		"documentId": result.Document.DocumentID,
		"title":      result.Version.Title, "summary": result.Version.Summary,
		"visibility": visibility(result.Policy.AuthenticatedPublic), "content": result.Version.Content,
		"createdAt": result.Document.CreatedAt.Unix(), "updatedAt": result.Document.UpdatedAt.Unix(),
	})
}

func (handler *Handler) trash(c *gin.Context) {
	ownerID, ok := identity.UserID(c)
	if !ok {
		writeUnauthorized(c)
		return
	}
	documentID := c.Param("documentId")
	if err := handler.commands.Trash(c.Request.Context(), application.TrashCommand{OwnerID: ownerID, DocumentID: documentID}); err != nil {
		writeApplicationError(c, err, "删除文章失败")
		return
	}
	responses.OK(c, gin.H{"documentId": documentID})
}

func (handler *Handler) get(c *gin.Context) {
	viewerID, ok := identity.UserID(c)
	if !ok {
		writeUnauthorized(c)
		return
	}
	view, err := handler.queries.Get(c.Request.Context(), viewerID, c.Param("documentId"))
	if err != nil {
		writeApplicationError(c, err, "获取文章失败")
		return
	}
	responses.OK(c, gin.H{
		"documentId": view.DocumentID, "title": view.Title, "summary": view.Summary,
		"visibility": visibility(view.AuthenticatedPublic), "content": view.Content,
		"createdAt": view.CreatedAt.Unix(), "updatedAt": view.UpdatedAt.Unix(),
	})
}

func (handler *Handler) listMine(c *gin.Context) {
	ownerID, ok := identity.UserID(c)
	if !ok {
		writeUnauthorized(c)
		return
	}
	query := new(pageQuery)
	if err := c.ShouldBindQuery(query); err != nil {
		responses.Fail(c, http.StatusBadRequest, baseerrors.CodeValidationFailed, "请检查分页参数")
		return
	}
	items, page, err := handler.queries.ListMine(c.Request.Context(), ownerID, query.Page, query.PageSize)
	if err != nil {
		writeApplicationError(c, err, "获取文章列表失败")
		return
	}
	writeList(c, items, page, "")
}

func (handler *Handler) listPublic(c *gin.Context) {
	query := new(pageQuery)
	if err := c.ShouldBindQuery(query); err != nil {
		responses.Fail(c, http.StatusBadRequest, baseerrors.CodeValidationFailed, "请检查分页参数")
		return
	}
	items, page, err := handler.queries.ListPublic(c.Request.Context(), query.Page, query.PageSize)
	if err != nil {
		writeApplicationError(c, err, "获取公开文章失败")
		return
	}
	writeList(c, items, page, "")
}

func (handler *Handler) searchMine(c *gin.Context) {
	ownerID, ok := identity.UserID(c)
	if !ok {
		writeUnauthorized(c)
		return
	}
	query := new(searchQuery)
	if err := c.ShouldBindQuery(query); err != nil {
		responses.Fail(c, http.StatusBadRequest, baseerrors.CodeValidationFailed, "请检查搜索参数")
		return
	}
	items, page, keyword, err := handler.queries.SearchMine(c.Request.Context(), ownerID, query.Keyword, query.Page, query.PageSize)
	if err != nil {
		if errors.Is(err, application.ErrQueryInvalidInput) {
			responses.Fail(c, http.StatusBadRequest, baseerrors.CodeValidationFailed, "搜索关键词不能为空且不能超过 100 个字符")
			return
		}
		slog.Error("document_search_failed", slog.Int64("owner_id", ownerID), slog.String("error", err.Error()))
		writeApplicationError(c, err, "搜索文稿失败，请稍后重试")
		return
	}
	writeList(c, items, page, keyword)
}

func writeList(c *gin.Context, items []domain.DocumentSummary, page application.Page, keyword string) {
	list := make([]documentSummaryResponse, 0, len(items))
	for _, item := range items {
		list = append(list, documentSummaryResponse{
			DocumentID: item.DocumentID, OwnerID: strconv.FormatInt(item.OwnerID, 10),
			Title: item.Title, Summary: item.Summary, Visibility: visibility(item.AuthenticatedPublic),
			CreatedAt: item.CreatedAt.Unix(), UpdatedAt: item.UpdatedAt.Unix(),
		})
	}
	data := gin.H{"documentList": list}
	if keyword != "" {
		data["keyword"] = keyword
	}
	responses.OKWithMeta(c, data, responses.NewPageMeta(page.Number, page.Size, int(page.Total)))
}

func visibility(public bool) string {
	if public {
		return "public"
	}
	return "private"
}
