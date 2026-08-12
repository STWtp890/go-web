package handler

import (
	"net/http"

	eror "gin-backend/internal/common/base/errors"
	"gin-backend/internal/common/base/responses"
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

	responses.OK(c, gin.H{
		"accessToken":  accessToken,
		"refreshToken": refreshToken,
	})
}
