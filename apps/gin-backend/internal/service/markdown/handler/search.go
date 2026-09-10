package handler

import (
	"log/slog"
	"net/http"

	eror "gin-backend/internal/common/base/errors"
	"gin-backend/internal/common/base/responses"
	"gin-backend/internal/common/service/jwt"
	logic "gin-backend/internal/service/markdown/logic"
	req "gin-backend/internal/service/markdown/types/requests"
	"gin-backend/internal/service/markdown/utils"

	"github.com/gin-gonic/gin"
)

// SearchHandler 全文搜索我的文章 (标题/摘要/正文, 按相关度排序)
// GET /api/v1/protected/markdown/search?keyword=xxx&page=1&pageSize=10
func SearchHandler(c *gin.Context) {
	// 1. 提取当前用户 (JWT claims sub)
	userID, ok := jwt.Subject(c)
	if !ok {
		responses.Fail(c, http.StatusUnauthorized, eror.CodeUnauthorized, "无法识别用户身份")
		return
	}

	// 2. 绑定查询参数
	q := new(req.SearchMarkdownQuery)
	if err := c.ShouldBindQuery(q); err != nil {
		responses.Fail(c, http.StatusBadRequest, eror.CodeValidationFailed, "请检查搜索参数")
		return
	}
	keyword, err := logic.NormalizeSearchKeyword(q.Keyword)
	if err != nil {
		responses.Fail(c, http.StatusBadRequest, eror.CodeValidationFailed, "搜索关键词不能为空且不能超过 100 个字符")
		return
	}

	// 3. 执行全文搜索
	list, total, err := logic.SearchMarkdownLogic(c.Request.Context(), userID, keyword, q.Page, q.PageSize)
	if err != nil {
		slog.Error("markdown_search_failed", slog.String("author_id", userID), slog.String("error", err.Error()))
		responses.Fail(c, http.StatusInternalServerError, eror.CodeInternalError, "搜索文稿失败，请稍后重试")
		return
	}

	// 4. 归一化分页并返回结果
	page, pageSize := utils.NormalizePage(q.Page, q.PageSize)
	totalPages := (int(total) + pageSize - 1) / pageSize
	responses.OKWithMeta(c, gin.H{"markdownList": list, "keyword": keyword}, &responses.Meta{
		Page:       page,
		PerPage:    pageSize,
		Total:      int(total),
		TotalPages: totalPages,
	})
}
