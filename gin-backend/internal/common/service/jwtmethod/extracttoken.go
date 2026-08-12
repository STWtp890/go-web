package jwtmethod

import (
	"strings"

	"github.com/gin-gonic/gin"
)

// ExtractToken 从请求中提取 JWT Token
// :Param
// - c: gin.Context
// :Return
// - string: 提取到的 JWT Token，如果未提供则返回空字符串
func ExtractToken(c *gin.Context) string {
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" {
		return ""
	}

	tokenStr := strings.TrimPrefix(authHeader, "Bearer")
	if tokenStr == "" {
		return ""
	}

	return strings.TrimSpace(tokenStr)
}