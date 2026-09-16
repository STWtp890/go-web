package handler

import (
	"gin-backend/internal/platform/httpserver/identity"

	"github.com/gin-gonic/gin"
)

// currentSubject 返回鉴权中间件注入的当前用户标识 (sub)
// :Return
// - `string` 用户 ID
// - `bool` 是否提取成功
func currentSubject(c *gin.Context) (string, bool) {
	principal, ok := identity.FromGin(c)
	if !ok || principal.Subject == "" {
		return "", false
	}
	return principal.Subject, true
}
