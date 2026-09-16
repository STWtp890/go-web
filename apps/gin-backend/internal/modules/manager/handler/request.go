// 审批流管理面 (ManagerAuthRequired 保护): 申请列表 / 通过 / 拒绝
package handler

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	eror "gin-backend/internal/common/base/errors"
	"gin-backend/internal/common/base/responses"
	logic "gin-backend/internal/modules/manager/logic"
	req "gin-backend/internal/modules/manager/types/requests"
	"gin-backend/internal/platform/httpserver/identity"

	"github.com/gin-gonic/gin"
)

// ListRequestsHandler 审批列表: GET /api/v1/protected/manager/requests?status=pending&page=1&pageSize=10
func ListRequestsHandler(c *gin.Context) {
	status := c.Query("status")

	q := new(req.ListRequestQuery)
	if err := c.ShouldBindQuery(q); err != nil {
		responses.Fail(c, http.StatusBadRequest, eror.CodeValidationFailed, "请检查分页参数")
		return
	}

	list, total, err := logic.ListRequestsLogic(c.Request.Context(), status, q.Page, q.PageSize)
	if err != nil {
		responses.Fail(c, http.StatusInternalServerError, eror.CodeInternalError, err.Error())
		return
	}

	// 归一化分页参数用于响应元信息
	page, pageSize := normalizePage(q.Page, q.PageSize)
	responses.OKWithMeta(c, gin.H{"requests": list}, responses.NewPageMeta(page, pageSize, int(total)))
}

// ApproveHandler 通过申请: POST /api/v1/protected/manager/requests/:id/approve
// 请求: {comment?}; 响应: {managerId, username, status}
func ApproveHandler(c *gin.Context) {
	reviewer, ok := identity.UserID(c)
	if !ok {
		responses.Fail(c, http.StatusUnauthorized, eror.CodeUnauthorized, "无效的Token")
		return
	}
	requestID := requestIDFromPath(c)
	if requestID == 0 {
		responses.Fail(c, http.StatusBadRequest, eror.CodeValidationFailed, "缺少申请单 ID")
		return
	}

	var rb req.ReviewRequest
	// comment 可选, 允许空 body
	err := c.ShouldBindJSON(&rb)
	if err != nil && !errors.Is(err, io.EOF) {
		responses.Fail(c, http.StatusBadRequest, eror.CodeValidationFailed, "请求参数错误")
		return
	}

	m, err := logic.ApproveLogic(c.Request.Context(), requestID, uint(reviewer), rb.Comment)
	if err != nil {
		failReview(c, err, true)
		return
	}

	responses.OK(c, gin.H{
		"managerId": m.ID,
		"username":  m.Username,
		"status":    m.Status,
	})
}

// RejectHandler 拒绝申请: POST /api/v1/protected/manager/requests/:id/reject
// 请求: {comment?}; 响应: {requestId, status}
func RejectHandler(c *gin.Context) {
	reviewer, ok := identity.UserID(c)
	if !ok {
		responses.Fail(c, http.StatusUnauthorized, eror.CodeUnauthorized, "无效的Token")
		return
	}
	requestID := requestIDFromPath(c)
	if requestID == 0 {
		responses.Fail(c, http.StatusBadRequest, eror.CodeValidationFailed, "缺少申请单 ID")
		return
	}

	var rb req.ReviewRequest
	if err := c.ShouldBindJSON(&rb); err != nil && !errors.Is(err, io.EOF) {
		responses.Fail(c, http.StatusBadRequest, eror.CodeValidationFailed, "请求参数错误")
		return
	}

	err := logic.RejectLogic(c.Request.Context(), requestID, uint(reviewer), rb.Comment)
	if err != nil {
		failReview(c, err, false)
		return
	}

	responses.OK(c, gin.H{"requestId": requestID, "status": "rejected"})
}

// normalizePage 归一化分页参数: page 至少 1, pageSize 落在 [1, 100]
func normalizePage(page, pageSize int) (int, int) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 10
	}
	return page, pageSize
}

// requestIDFromPath 从路径参数解析申请单 ID (uint); 缺失/非法返回 0
func requestIDFromPath(c *gin.Context) uint {
	p := c.Param("id")
	if p == "" {
		return 0
	}
	id, err := strconv.ParseUint(p, 10, 64)
	if err != nil {
		return 0
	}
	return uint(id)
}
