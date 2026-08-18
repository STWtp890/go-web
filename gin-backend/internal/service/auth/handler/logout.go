package handler

import (
	"net/http"

	eror "gin-backend/internal/common/base/errors"
	"gin-backend/internal/common/base/responses"
	logic "gin-backend/internal/service/auth/logic"

	"github.com/gin-gonic/gin"
	jwtlib "github.com/golang-jwt/jwt/v5"
)

// LogoutHandler 用户登出
func LogoutHandler(c *gin.Context) {
	// 1. 提取中间件注入的 claims
	claims, ok := c.Get("claims")
	if !ok {
		responses.Fail(c, http.StatusUnauthorized, eror.CodeUnauthorized, "未登录")
		return
	}
	mapClaims, ok := claims.(jwtlib.MapClaims)
	if !ok {
		responses.Fail(c, http.StatusInternalServerError, eror.CodeInternalError, "token 解析异常")
		return
	}

	// 2. 执行登出逻辑
	if err := logic.LogoutLogic(c.Request.Context(), mapClaims); err != nil {
		responses.Fail(c, http.StatusInternalServerError, eror.CodeInternalError, err.Error())
		return
	}

	responses.OK(c, gin.H{"message": "登出成功"})
}
