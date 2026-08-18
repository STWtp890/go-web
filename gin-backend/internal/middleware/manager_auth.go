// 管理员 JWT 鉴权中间件: 管理面 (manager 审批流) 专用
package middleware

import (
	"log/slog"
	"net/http"

	eror "gin-backend/internal/common/base/errors"
	"gin-backend/internal/common/base/responses"
	"gin-backend/internal/common/service/jwt"
	"gin-backend/internal/config"

	"github.com/gin-gonic/gin"
	jwtlib "github.com/golang-jwt/jwt/v5"
)

// ManagerAuthRequired 管理员 JWT 鉴权中间件
// 校验: RS256 验签 + role=manager + sid 为管理员当前有效会话。
func ManagerAuthRequired(conf *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 1. 提取 JWT Token
		tokenStr := jwt.ExtractToken(c)
		if tokenStr == "" {
			responses.Fail(c, http.StatusUnauthorized, eror.CodeUnauthorized, "无效的Token")
			return
		}

		// 2. 解析 JWT Token
		jwtToken, err := jwt.ParseToken(tokenStr, config.CustomConfig().JWT.GetPublicKey(), jwt.TokenUseAccess)
		if err != nil {
			responses.Fail(c, http.StatusUnauthorized, eror.CodeUnauthorized, "Token解析失败")
			return
		}

		// 3. 校验 claims 与角色
		claims, ok := jwtToken.Claims.(jwtlib.MapClaims)
		if !ok || !jwtToken.Valid {
			responses.Fail(c, http.StatusUnauthorized, eror.CodeUnauthorized, "Token claims 无效")
			return
		}
		if role, _ := claims["role"].(string); role != "manager" {
			responses.Fail(c, http.StatusForbidden, eror.CodeForbidden, "非管理员, 无权访问")
			return
		}

		// 4. 校验 sid 为管理员当前会话。新登录、登出或强制撤销后，旧
		// Access Token 无需逐个加入黑名单也会立即失效。
		sub, err := claims.GetSubject()
		sid, hasSID := jwt.SessionIDFromClaims(claims)
		if err != nil || sub == "" || !hasSID {
			responses.Fail(c, http.StatusUnauthorized, eror.CodeUnauthorized, "会话已失效，请重新登录")
			return
		}
		active, err := jwt.IsManagerSessionActive(c.Request.Context(), sub, sid)
		if err != nil {
			slog.Error("manager_session_check_failed", slog.String("error", err.Error()))
			responses.Fail(c, http.StatusServiceUnavailable, eror.CodeServiceUnavail, "认证服务暂不可用")
			return
		}
		if !active {
			responses.Fail(c, http.StatusUnauthorized, eror.CodeUnauthorized, "会话已失效，请重新登录")
			return
		}

		// 5. 注入 context (值类型, 与 ExtractClaims 一致)
		c.Set("claims", claims)
		c.Next()
	}
}
