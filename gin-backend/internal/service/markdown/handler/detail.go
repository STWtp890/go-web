package handler

import (
	"errors"
	"net/http"

	eror "gin-backend/internal/common/base/errors"
	"gin-backend/internal/common/base/responses"
	logic "gin-backend/internal/service/markdown/logic"

	"github.com/gin-gonic/gin"
)

// DetailHandler 文章详情 (元信息 + 完整 content)
// GET /api/v1/protected/markdown/:markdownId
// 可见性: public 任意登录用户可读; private 仅作者本人 (403)
func DetailHandler(c *gin.Context) {
	// 1. 提取当前用户 (JWT claims sub)
	userID, ok := currentUserID(c)
	if !ok {
		responses.Fail(c, http.StatusUnauthorized, eror.CodeUnauthorized, "无法识别用户身份")
		return
	}

	// 2. 提取路径参数
	markdownID := c.Param("markdownId")
	if markdownID == "" {
		responses.Fail(c, http.StatusBadRequest, eror.CodeValidationFailed, "缺少文章ID")
		return
	}

	// 3. 执行详情逻辑 (可见性校验在 logic)
	md, content, err := logic.GetMarkdownLogic(c.Request.Context(), userID, markdownID)
	if err != nil {
		if errors.Is(err, logic.ErrMarkdownForbidden) {
			responses.Fail(c, http.StatusForbidden, eror.CodeForbidden, "无权查看该文章")
			return
		}
		responses.Fail(c, http.StatusNotFound, eror.CodeNotFound, err.Error())
		return
	}

	// 4. 返回详情
	responses.OK(c, gin.H{
		"markdownId": md.MarkdownID,
		"title":      md.Title,
		"summary":    md.Summary,
		"visibility": md.Visibility,
		"content":    content,
		"createdAt":  md.CreatedAt,
		"updatedAt":  md.UpdatedAt,
	})
}
