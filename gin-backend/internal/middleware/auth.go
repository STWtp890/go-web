package middleware

import (
	"net/http"

	eror "gin-backend/internal/common/base/errors"
	"gin-backend/internal/config"
	logic "gin-backend/internal/service/auth/logic"

	"gin-backend/internal/common/base/responses"
	"gin-backend/internal/common/service/jwtmethod"

	"github.com/gin-gonic/gin"
)

// AuthRequired JWT 鉴权中间件
func AuthRequired(conf *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 1. 提取 JWT Token
		tokenStr := jwtmethod.ExtractToken(c)
		if tokenStr == "" {
			responses.Fail(c, http.StatusUnauthorized, eror.CodeUnauthorized, "无效的Token")
			return
		}

		// 2.1 解析 JWT Token
		jwtToken, err := jwtmethod.ParseToken(tokenStr, config.CustomConfig().JWT.GetPublicKey())
		if err != nil {
			responses.Fail(c, http.StatusUnauthorized, eror.CodeUnauthorized, "Token解析失败")
			return
		}

		// 2.2 检查 Access Token 黑名单（登出/吊销后拒绝）
		if logic.IsAccessBlacklisted(c.Request.Context(), tokenStr) {
			responses.Fail(c, http.StatusUnauthorized, eror.CodeUnauthorized, "已吊销的Token")
			return
		}

		// 3. 注入 context 用户信息
		c.Set("claims", jwtToken.Claims)

		// 4. 继续处理请求
		c.Next()
	}
}
