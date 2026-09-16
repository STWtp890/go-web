package middleware

import (
	"net/http"

	eror "gin-backend/internal/common/base/errors"
	"gin-backend/internal/common/base/responses"
	"gin-backend/internal/common/service/sessioncookie"
	"gin-backend/internal/platform/httpserver/identity"

	"github.com/gin-gonic/gin"
)

// CSRFProtection 保护基于浏览器 Cookie 认证的非安全请求。
//
// 会话标识直接读取鉴权中间件注入的 identity.Principal, 不再重复解析 JWT claims。
func CSRFProtection(csrfCookieName string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead || c.Request.Method == http.MethodOptions {
			c.Next()
			return
		}
		sessionID, ok := identity.SessionID(c)
		if !ok || !sessioncookie.ValidateCSRF(c, csrfCookieName, sessionID) {
			responses.AbortFail(c, http.StatusForbidden, eror.CodeForbidden, "CSRF 校验失败")
			return
		}
		c.Next()
	}
}
