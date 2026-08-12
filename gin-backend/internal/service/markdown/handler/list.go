package handler

import (
	"net/http"

	eror "gin-backend/internal/common/base/errors"
	"gin-backend/internal/common/base/responses"
	logic "gin-backend/internal/service/markdown/logic"
	req "gin-backend/internal/service/markdown/types/requests"

	"github.com/gin-gonic/gin"
)

// ListMyHandler 我的文章分页列表
// GET /api/v1/protected/markdown/mine?page=1&pageSize=10
func ListMyHandler(c *gin.Context) {
	// 1. 提取当前用户 (JWT claims sub)
	userID, ok := currentUserID(c)
	if !ok {
		responses.Fail(c, http.StatusUnauthorized, eror.CodeUnauthorized, "无法识别用户身份")
		return
	}

	// 2. 绑定分页查询参数 (缺省/非法值由 logic 归一化)
	q := new(req.ListMyMarkdownQuery)
	if err := c.ShouldBindQuery(q); err != nil {
		responses.Fail(c, http.StatusBadRequest, eror.CodeValidationFailed, "请检查分页参数")
		return
	}

	// 3. 执行列表逻辑
	list, total, err := logic.ListMyMarkdownLogic(c.Request.Context(), userID, q.Page, q.PageSize)
	if err != nil {
		responses.Fail(c, http.StatusInternalServerError, eror.CodeInternalError, err.Error())
		return
	}

	// 4. 归一化分页参数用于响应元信息
	page, pageSize := normalizePage(q.Page, q.PageSize)

	// 5. 返回分页结果
	totalPages := (int(total) + pageSize - 1) / pageSize
	responses.OKWithMeta(c, gin.H{"markdownList": list}, &responses.Meta{
		Page:       page,
		PerPage:    pageSize,
		Total:      int(total),
		TotalPages: totalPages,
	})
}
