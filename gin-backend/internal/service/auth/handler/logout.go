package handler

import (
	"net/http"

	eror "gin-backend/internal/common/base/errors"
	"gin-backend/internal/common/base/responses"
	logic "gin-backend/internal/service/auth/logic"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// LogoutHandler 用户登出
func LogoutHandler(c *gin.Context) {
	// 1. 提取 access token（与 AuthRequired 中间件相同的提取逻辑）
	accessToken := extractTokenFromHeader(c)

	// 2. 提取中间件注入的 claims
	claims, ok := c.Get("claims")
	if !ok {
		responses.Fail(c, http.StatusUnauthorized, eror.CodeUnauthorized, "未登录")
		return
	}
	mapClaims, ok := claims.(jwt.MapClaims)
	if !ok {
		responses.Fail(c, http.StatusInternalServerError, eror.CodeInternalError, "token 解析异常")
		return
	}

	// 3. 执行登出逻辑
	if err := logic.LogoutLogic(c.Request.Context(), accessToken, mapClaims); err != nil {
		responses.Fail(c, http.StatusInternalServerError, eror.CodeInternalError, err.Error())
		return
	}

	responses.OK(c, gin.H{"message": "登出成功"})
}

// extractTokenFromHeader 从 Authorization header 提取 Bearer token
func extractTokenFromHeader(c *gin.Context) string {
	authHeader := c.GetHeader("Authorization")
	if len(authHeader) > 7 && authHeader[:7] == "Bearer " {
		return authHeader[7:]
	}
	return ""
}
