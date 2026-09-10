// 提交管理员注册申请 (公开, 无鉴权)
package handler

import (
	"errors"
	"net/http"

	eror "gin-backend/internal/common/base/errors"
	"gin-backend/internal/common/base/responses"
	logic "gin-backend/internal/service/manager/logic"
	"gin-backend/internal/service/manager/types/constant"
	req "gin-backend/internal/service/manager/types/requests"

	"github.com/gin-gonic/gin"
)

// RegisterHandler 提交管理员注册申请: POST /api/v1/public/manager/register
// 请求: {username, password, email?, reason?}; 响应: {requestId, username, status}
func RegisterHandler(c *gin.Context) {
	r := new(req.RegisterRequest)
	if err := c.ShouldBindJSON(r); err != nil {
		responses.Fail(c, http.StatusBadRequest, eror.CodeValidationFailed, "请检查输入参数")
		return
	}

	rr, err := logic.RegisterLogic(c.Request.Context(), r)
	if err != nil {
		if errors.Is(err, constant.ErrUsernameTaken) {
			responses.Fail(c, http.StatusConflict, eror.CodeConflict, "用户名已被占用")
			return
		}
		responses.Fail(c, http.StatusInternalServerError, eror.CodeInternalError, "提交申请失败")
		return
	}

	responses.Created(c, gin.H{
		"requestId": rr.ID,
		"username":  rr.Username,
		"status":    rr.Status,
	})
}
