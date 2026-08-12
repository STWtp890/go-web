// Package api 注册 markdown 业务路由
package api

import (
	"gin-backend/internal/service/markdown/handler"

	"github.com/gin-gonic/gin"
)

// SetRouteGroup 注册 markdown 业务路由组
// :Param
// - `public` 公开路由组 (无鉴权)
// - `protected` 保护路由组 (需鉴权)
func SetRouteGroup(public, protected *gin.RouterGroup) {
	// 保护路由组: /api/v1/protected/markdown
	mdProtected := protected.Group("markdown")
	mdProtected.POST("/upload", handler.UploadHandler)     // POST /api/v1/protected/markdown/upload  上传文章 (可指定可见性)
	mdProtected.GET("/mine", handler.ListMyHandler)        // GET  /api/v1/protected/markdown/mine    我的文章列表
	mdProtected.GET("/public", handler.PublicListHandler)  // GET  /api/v1/protected/markdown/public  公开文章列表 (所有登录用户)
	mdProtected.GET("/search", handler.SearchHandler)      // GET  /api/v1/protected/markdown/search  全文搜索
	mdProtected.GET("/:markdownId", handler.DetailHandler) // GET  /api/v1/protected/markdown/:markdownId 文章详情 (public 可见/private 仅作者)
}
