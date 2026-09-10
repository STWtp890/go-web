package handler

import (
	"net/http"

	eror "gin-backend/internal/common/base/errors"
	"gin-backend/internal/common/base/responses"
	"gin-backend/internal/common/service/sessioncookie"
	logic "gin-backend/internal/service/auth/logic"
	req "gin-backend/internal/service/auth/types/requests"

	"github.com/gin-gonic/gin"
)

// LoginHandler 用户登录
func LoginHandler(c *gin.Context) {
	req := new(req.LoginRequest)
	if err := c.ShouldBindJSON(req); err != nil {
		responses.Fail(c, http.StatusBadRequest, eror.CodeParseError, "输入参数解析失败")
		return
	}

	accessToken, refreshToken, err := logic.LoginLogic(c.Request.Context(), req)
	if err != nil {
		responses.Fail(c, http.StatusUnauthorized, eror.CodeUnauthorized, err.Error())
		return
	}
	if err := sessioncookie.SetUserTokens(c, accessToken, refreshToken); err != nil {
		responses.Fail(c, http.StatusInternalServerError, eror.CodeInternalError, "登录会话写入失败")
		return
	}

	responses.OK(c, gin.H{"message": "登录成功"})
}
