package middleware

import (
	"net/http"

	eror "gin-backend/internal/common/base/errors"
	"gin-backend/internal/common/base/responses"
	"gin-backend/internal/common/service/jwt"
	"gin-backend/internal/common/service/sessioncookie"

	"github.com/gin-gonic/gin"
	jwtlib "github.com/golang-jwt/jwt/v5"
)

// CSRFProtection 保护基于浏览器 Cookie 认证的非安全请求。
func CSRFProtection(csrfCookieName string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead || c.Request.Method == http.MethodOptions {
			c.Next()
			return
		}
		claims, ok := c.Get("claims")
		mapClaims, valid := claims.(jwtlib.MapClaims)
		sid, hasSID := jwt.SessionIDFromClaims(mapClaims)
		if !ok || !valid || !hasSID || !sessioncookie.ValidateCSRF(c, csrfCookieName, sid) {
			responses.Fail(c, http.StatusForbidden, eror.CodeForbidden, "CSRF 校验失败")
			return
		}
		c.Next()
	}
}
