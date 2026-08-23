package middleware

import (
	"log/slog"
	"net/http"

	eror "gin-backend/internal/common/base/errors"
	"gin-backend/internal/common/base/responses"
	"gin-backend/internal/common/service/jwt"
	"gin-backend/internal/common/service/sessioncookie"
	"gin-backend/internal/config"

	"github.com/gin-gonic/gin"
	jwtlib "github.com/golang-jwt/jwt/v5"
)

// AuthRequired JWT 鉴权中间件
func AuthRequired(conf *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 1. 提取 JWT Token
		tokenStr := sessioncookie.AccessToken(c, sessioncookie.UserAccessCookie)
		if tokenStr == "" {
			responses.AbortFail(c, http.StatusUnauthorized, eror.CodeUnauthorized, "无效的Token")
			return
		}

		// 2.1 解析 JWT Token
		jwtToken, err := jwt.ParseToken(tokenStr, config.CustomConfig().JWT.GetPublicKey(), jwt.TokenUseAccess)
		if err != nil {
			responses.AbortFail(c, http.StatusUnauthorized, eror.CodeUnauthorized, "Token解析失败")
			return
		}

		// 2.2 校验 sid 仍是该用户的当前有效会话。新登录覆盖 sid 后，
		// 旧 Access Token 无需逐个加入黑名单也会立即失效。
		claims, ok := jwtToken.Claims.(jwtlib.MapClaims)
		if !ok {
			responses.AbortFail(c, http.StatusUnauthorized, eror.CodeUnauthorized, "Token解析失败")
			return
		}
		sub, err := claims.GetSubject()
		sid, hasSID := jwt.SessionIDFromClaims(claims)
		if err != nil || sub == "" || !hasSID {
			responses.AbortFail(c, http.StatusUnauthorized, eror.CodeUnauthorized, "会话已失效，请重新登录")
			return
		}
		active, err := jwt.IsUserSessionActive(c.Request.Context(), sub, sid)
		if err != nil {
			slog.Error("jwt_session_check_failed", slog.String("error", err.Error()))
			responses.AbortFail(c, http.StatusServiceUnavailable, eror.CodeServiceUnavail, "认证服务暂不可用")
			return
		}
		if !active {
			responses.AbortFail(c, http.StatusUnauthorized, eror.CodeUnauthorized, "会话已失效，请重新登录")
			return
		}

		// 3. 注入 context 用户信息
		// 注入 Claims 的值类型, 避免外部修改 (如 `type MapClaims map[string]any`)
		c.Set("claims", claims)
		// 4. 继续处理请求
		c.Next()
	}
}
