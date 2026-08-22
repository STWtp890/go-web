// 管理员登录 (公开, 无鉴权)
package handler

import (
	"net/http"

	eror "gin-backend/internal/common/base/errors"
	"gin-backend/internal/common/base/responses"
	"gin-backend/internal/common/service/sessioncookie"
	logic "gin-backend/internal/service/manager/logic"
	req "gin-backend/internal/service/manager/types/requests"

	"github.com/gin-gonic/gin"
)

// LoginHandler 管理员登录: POST /api/v1/public/manager/login
// 请求: {username, password}; 成功后通过 HttpOnly Cookie 建立会话。
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
	if err := sessioncookie.SetManagerTokens(c, accessToken, refreshToken); err != nil {
		responses.Fail(c, http.StatusInternalServerError, eror.CodeInternalError, "登录会话写入失败")
		return
	}

	responses.OK(c, gin.H{"message": "登录成功"})
}
