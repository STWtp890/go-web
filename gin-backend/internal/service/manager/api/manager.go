// Package api 注册 manager (管理员注册审批流) 业务路由
package api

import (
	"gin-backend/internal/service/manager/handler"

	"github.com/gin-gonic/gin"
)

// SetRouteGroup 注册 manager 业务路由组
// :Param
// - `public` 公开路由组 (无鉴权)
// - `managerProtected` 管理员保护路由组 (ManagerAuthRequired)
func SetRouteGroup(public, managerProtected *gin.RouterGroup) {
	// 公开: 提交注册申请 / 管理员登录
	managerPublic := public.Group("manager")
	managerPublic.POST("/register", handler.RegisterHandler) // POST /api/v1/public/manager/register  提交注册申请
	managerPublic.POST("/login", handler.LoginHandler)       // POST /api/v1/public/manager/login     管理员登录
	managerPublic.POST("/refresh", handler.RefreshHandler)

	// 管理员保护: 鉴权会话 + 审批流管理面
	manager := managerProtected.Group("manager")
	manager.POST("/logout", handler.LogoutHandler)
	manager.POST("/requests/:id/approve", handler.ApproveHandler)
	manager.POST("/requests/:id/reject", handler.RejectHandler)
	manager.GET("/requests", handler.ListRequestsHandler)
}
