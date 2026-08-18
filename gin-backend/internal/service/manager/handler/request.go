// 审批流管理面 (ManagerAuthRequired 保护): 申请列表 / 通过 / 拒绝
package handler

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	eror "gin-backend/internal/common/base/errors"
	"gin-backend/internal/common/base/responses"
	"gin-backend/internal/common/service/jwt"
	logic "gin-backend/internal/service/manager/logic"
	tc "gin-backend/internal/service/manager/types/constant"
	req "gin-backend/internal/service/manager/types/requests"

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
	totalPages := (int(total) + pageSize - 1) / pageSize
	responses.OKWithMeta(c, gin.H{"requests": list}, &responses.Meta{
		Page:       page,
		PerPage:    pageSize,
		Total:      int(total),
		TotalPages: totalPages,
	})
}

// ApproveHandler 通过申请: POST /api/v1/protected/manager/requests/:id/approve
// 请求: {comment?}; 响应: {managerId, username, status}
func ApproveHandler(c *gin.Context) {
	reviewerID, ok := jwt.SubjectUint(c)
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

	m, err := logic.ApproveLogic(c.Request.Context(), requestID, reviewerID, rb.Comment)
	if err != nil {
		switch {
		case errors.Is(err, tc.ErrRequestNotFound):
			responses.Fail(c, http.StatusNotFound, eror.CodeNotFound, "申请单不存在")
		case errors.Is(err, tc.ErrRequestReviewed):
			responses.Fail(c, http.StatusConflict, eror.CodeConflict, "申请单已审批")
		case errors.Is(err, tc.ErrUsernameTaken):
			responses.Fail(c, http.StatusConflict, eror.CodeConflict, "用户名已被占用")
		default:
			responses.Fail(c, http.StatusInternalServerError, eror.CodeInternalError, "审批失败")
		}
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
	reviewerID, ok := jwt.SubjectUint(c)
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

	err := logic.RejectLogic(c.Request.Context(), requestID, reviewerID, rb.Comment)
	if err != nil {
		switch {
		case errors.Is(err, tc.ErrRequestNotFound):
			responses.Fail(c, http.StatusNotFound, eror.CodeNotFound, "申请单不存在")
		case errors.Is(err, tc.ErrRequestReviewed):
			responses.Fail(c, http.StatusConflict, eror.CodeConflict, "申请单已审批")
		default:
			responses.Fail(c, http.StatusInternalServerError, eror.CodeInternalError, "审批失败")
		}
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
