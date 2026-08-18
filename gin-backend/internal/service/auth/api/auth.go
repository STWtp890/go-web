// Package api 注册 auth 业务路由
package api

import (
	"gin-backend/internal/service/auth/handler"

	"github.com/gin-gonic/gin"
)

// SetRouteGroup 注册 auth 业务路由组
// :Param
// - `public` 公开路由组 (无鉴权)
// - `protected` 保护路由组 (需鉴权)
func SetRouteGroup(public, protected *gin.RouterGroup) {
	// 公开路由组: /api/v1/public/auth
	authPublic := public.Group("auth")
	authPublic.POST("/login", handler.LoginHandler)          // POST /api/v1/public/auth/login
	authPublic.POST("/register", handler.RegisterHandler)    // POST /api/v1/public/auth/register
	authPublic.POST("/refresh", handler.RefreshTokenHandler) // refresh token 自行验签与白名单校验

	// 保护路由组: /api/v1/protected/auth
	authProtected := protected.Group("auth")
	authProtected.POST("/logout", handler.LogoutHandler) // POST /api/v1/protected/auth/logout
}
