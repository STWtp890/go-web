package handler

import (
	"net/http"

	eror "gin-backend/internal/common/base/errors"
	"gin-backend/internal/common/base/responses"
	logic "gin-backend/internal/service/markdown/logic"

	"github.com/gin-gonic/gin"
)

// DetailHandler 文章详情 (元信息 + 完整 content)
// GET /api/v1/protected/markdown/:markdownId
// 归属校验: 仅当前登录用户可读取自己的文章 (防越权 IDOR)
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

	// 3. 执行详情逻辑 (仅本人文章)
	md, content, err := logic.GetMarkdownLogic(c.Request.Context(), userID, markdownID)
	if err != nil {
		responses.Fail(c, http.StatusNotFound, eror.CodeNotFound, err.Error())
		return
	}

	// 3. 返回详情
	responses.OK(c, gin.H{
		"markdownId": md.MarkdownID,
		"title":      md.Title,
		"summary":    md.Summary,
		"content":    content,
		"createdAt":  md.CreatedAt,
		"updatedAt":  md.UpdatedAt,
	})
}
