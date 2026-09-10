package handler

import (
	"errors"
	"net/http"

	eror "gin-backend/internal/common/base/errors"
	"gin-backend/internal/common/base/responses"
	"gin-backend/internal/common/service/jwt"
	logic "gin-backend/internal/service/markdown/logic"
	req "gin-backend/internal/service/markdown/types/requests"

	"github.com/gin-gonic/gin"
)

// UpdateHandler 全量更新当前用户自己的文章。
// PUT /api/v1/protected/markdown/:markdownId
func UpdateHandler(c *gin.Context) {
	userID, ok := jwt.Subject(c)
	if !ok {
		responses.Fail(c, http.StatusUnauthorized, eror.CodeUnauthorized, "无法识别用户身份")
		return
	}
	markdownID := c.Param("markdownId")
	if markdownID == "" {
		responses.Fail(c, http.StatusBadRequest, eror.CodeValidationFailed, "缺少文章ID")
		return
	}

	r := new(req.UpdateMarkdownRequest)
	if err := c.ShouldBindJSON(r); err != nil {
		responses.Fail(c, http.StatusBadRequest, eror.CodeValidationFailed, "请检查输入参数")
		return
	}
	md, err := logic.UpdateMarkdownLogic(c.Request.Context(), userID, markdownID, r)
	if err != nil {
		writeMutationError(c, err, "保存文章失败")
		return
	}

	responses.OK(c, gin.H{
		"markdownId": md.MarkdownID,
		"title":      md.Title,
		"summary":    md.Summary,
		"visibility": md.Visibility,
		"content":    r.Content,
		"createdAt":  md.CreatedAt,
		"updatedAt":  md.UpdatedAt,
	})
}

// DeleteHandler 删除当前用户自己的文章。
// DELETE /api/v1/protected/markdown/:markdownId
func DeleteHandler(c *gin.Context) {
	userID, ok := jwt.Subject(c)
	if !ok {
		responses.Fail(c, http.StatusUnauthorized, eror.CodeUnauthorized, "无法识别用户身份")
		return
	}
	markdownID := c.Param("markdownId")
	if markdownID == "" {
		responses.Fail(c, http.StatusBadRequest, eror.CodeValidationFailed, "缺少文章ID")
		return
	}

	if err := logic.DeleteMarkdownLogic(c.Request.Context(), userID, markdownID); err != nil {
		writeMutationError(c, err, "删除文章失败")
		return
	}
	responses.OK(c, gin.H{"markdownId": markdownID})
}

func writeMutationError(c *gin.Context, err error, internalMessage string) {
	switch {
	case errors.Is(err, logic.ErrMarkdownInvalidInput):
		responses.Fail(c, http.StatusBadRequest, eror.CodeValidationFailed, err.Error())
	case errors.Is(err, logic.ErrMarkdownNotFound):
		responses.Fail(c, http.StatusNotFound, eror.CodeNotFound, "文章不存在")
	case errors.Is(err, logic.ErrMarkdownForbidden):
		responses.Fail(c, http.StatusForbidden, eror.CodeForbidden, "无权操作该文章")
	default:
		responses.Fail(c, http.StatusInternalServerError, eror.CodeInternalError, internalMessage)
	}
}
