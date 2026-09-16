package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"

	eror "gin-backend/internal/common/base/errors"
	"gin-backend/internal/common/base/responses"
	"gin-backend/internal/common/service/jwt"
	"gin-backend/internal/common/service/sessioncookie"
	"gin-backend/internal/config"
	"gin-backend/internal/platform/httpserver/identity"

	"github.com/gin-gonic/gin"
	jwtlib "github.com/golang-jwt/jwt/v5"
)

// authPolicy 描述一个账号域的鉴权差异。
//
// 用户面与管理面的验签、claims 校验、会话校验和身份注入流程完全一致, 差异只体现在
// 下列取值上; 因此两者共用同一实现, 不再各维护一份中间件。
type authPolicy struct {
	kind          identity.Kind
	accessCookie  string
	requiredRole  string
	sessionActive func(ctx context.Context, subject, sessionID string) (bool, error)
}

var (
	userAuthPolicy = authPolicy{
		kind:         identity.KindUser,
		accessCookie: sessioncookie.UserAccessCookie,
		sessionActive: func(ctx context.Context, subject, sessionID string) (bool, error) {
			return jwt.IsUserSessionActive(ctx, subject, sessionID)
		},
	}
	managerAuthPolicy = authPolicy{
		kind:         identity.KindManager,
		accessCookie: sessioncookie.ManagerAccessCookie,
		requiredRole: "manager",
		sessionActive: func(ctx context.Context, subject, sessionID string) (bool, error) {
			return jwt.IsManagerSessionActive(ctx, subject, sessionID)
		},
	}
)

// AuthRequired 前台用户 JWT 鉴权中间件。
func AuthRequired(_ *config.Config) gin.HandlerFunc {
	return authenticate(userAuthPolicy)
}

// ManagerAuthRequired 管理面 JWT 鉴权中间件。
func ManagerAuthRequired(_ *config.Config) gin.HandlerFunc {
	return authenticate(managerAuthPolicy)
}

// authenticate 是唯一的鉴权实现:
// 提取 Cookie → 验签 → 校验角色与 sid → 校验会话仍活跃 → 注入 identity.Principal → 放行。
//
// sid 校验的意义: 新登录覆盖 sid 后, 旧 Access Token 无需逐个加入黑名单也立即失效。
func authenticate(policy authPolicy) gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenStr := sessioncookie.AccessToken(c, policy.accessCookie)
		if tokenStr == "" {
			responses.AbortFail(c, http.StatusUnauthorized, eror.CodeUnauthorized, "无效的Token")
			return
		}

		jwtToken, err := jwt.ParseToken(tokenStr, config.CustomConfig().JWT.GetPublicKey(), jwt.TokenUseAccess)
		if err != nil {
			responses.AbortFail(c, http.StatusUnauthorized, eror.CodeUnauthorized, "Token解析失败")
			return
		}

		claims, ok := jwtToken.Claims.(jwtlib.MapClaims)
		if !ok || !jwtToken.Valid {
			responses.AbortFail(c, http.StatusUnauthorized, eror.CodeUnauthorized, "Token claims 无效")
			return
		}
		if policy.requiredRole != "" {
			if role, _ := claims["role"].(string); role != policy.requiredRole {
				responses.AbortFail(c, http.StatusForbidden, eror.CodeForbidden, "非管理员, 无权访问")
				return
			}
		}

		subject, err := claims.GetSubject()
		sessionID, hasSession := jwt.SessionIDFromClaims(claims)
		if err != nil || subject == "" || !hasSession {
			responses.AbortFail(c, http.StatusUnauthorized, eror.CodeUnauthorized, "会话已失效，请重新登录")
			return
		}

		active, err := policy.sessionActive(c.Request.Context(), subject, sessionID)
		if err != nil {
			slog.Error("jwt_session_check_failed",
				slog.String("error", err.Error()),
				slog.String("kind", string(policy.kind)),
			)
			responses.AbortFail(c, http.StatusServiceUnavailable, eror.CodeServiceUnavail, "认证服务暂不可用")
			return
		}
		if !active {
			responses.AbortFail(c, http.StatusUnauthorized, eror.CodeUnauthorized, "会话已失效，请重新登录")
			return
		}

		identity.Set(c, identity.Principal{
			Kind:      policy.kind,
			Subject:   subject,
			UserID:    numericSubject(subject),
			SessionID: sessionID,
		})
		c.Next()
	}
}

// numericSubject 把 JWT sub 解析为规范化数值; 无法解析或非正数时返回 0。
func numericSubject(subject string) int64 {
	value, err := strconv.ParseInt(subject, 10, 64)
	if err != nil || value <= 0 {
		return 0
	}
	return value
}
