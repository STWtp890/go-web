package handler

import (
	"net/http"

	eror "gin-backend/internal/common/base/errors"
	"gin-backend/internal/common/base/responses"
	logic "gin-backend/internal/service/markdown/logic"
	req "gin-backend/internal/service/markdown/types/requests"

	"github.com/gin-gonic/gin"
)

// UploadHandler 上传文章 (基于当前登录用户)
// POST /api/v1/protected/markdown/upload
func UploadHandler(c *gin.Context) {
	// 1. 提取当前用户 (JWT claims sub)
	userID, ok := currentUserID(c)
	if !ok {
		responses.Fail(c, http.StatusUnauthorized, eror.CodeUnauthorized, "无法识别用户身份")
		return
	}

	// 2. 绑定请求参数
	r := new(req.UploadMarkdownRequest)
	if err := c.ShouldBindJSON(r); err != nil {
		responses.Fail(c, http.StatusBadRequest, eror.CodeValidationFailed, "请检查输入参数")
		return
	}

	// 3. 执行上传逻辑
	md, err := logic.UploadMarkdownLogic(c.Request.Context(), userID, r)
	if err != nil {
		responses.Fail(c, http.StatusBadRequest, eror.CodeValidationFailed, err.Error())
		return
	}

	// 4. 返回创建结果
	responses.Created(c, gin.H{
		"markdownId": md.MarkdownID,
		"title":      md.Title,
		"summary":    md.Summary,
		"visibility": md.Visibility,
		"createdAt":  md.CreatedAt,
		"updatedAt":  md.UpdatedAt,
	})
}
