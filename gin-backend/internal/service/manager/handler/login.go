// 管理员登录 (公开, 无鉴权)
package handler

import (
	"net/http"

	eror "gin-backend/internal/common/base/errors"
	"gin-backend/internal/common/base/responses"
	logic "gin-backend/internal/service/manager/logic"
	req "gin-backend/internal/service/manager/types/requests"

	"github.com/gin-gonic/gin"
)

// LoginHandler 管理员登录: POST /api/v1/public/manager/login
// 请求: {username, password}; 响应: {accessToken} (role=manager)
func LoginHandler(c *gin.Context) {
	r := new(req.LoginRequest)
	if err := c.ShouldBindJSON(r); err != nil {
		responses.Fail(c, http.StatusBadRequest, eror.CodeParseError, "输入参数解析失败")
		return
	}

	accessToken, refreshToken, err := logic.LoginLogic(c.Request.Context(), r)
	if err != nil {
		responses.Fail(c, http.StatusUnauthorized, eror.CodeUnauthorized, err.Error())
		return
	}

	responses.OK(c, gin.H{
		"accessToken":  accessToken,
		"refreshToken": refreshToken,
	})
}
