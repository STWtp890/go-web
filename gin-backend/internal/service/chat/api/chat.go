// Package api 注册 chat 业务路由
package api

import (
	"gin-backend/internal/service/chat/handler"

	"github.com/gin-gonic/gin"
)

// SetRouteGroup 注册 chat 业务路由组
// :Param
// - `protected` 保护路由组 (需鉴权)
func SetRouteGroup(protected *gin.RouterGroup) {
	// 保护路由组: /api/v1/protected/chat
	chatProtected := protected.Group("chat")
	chatProtected.GET("/ws", handler.WebSocketHandler)          // GET /api/v1/protected/chat/ws
	chatProtected.GET("/sse", handler.SSEHandler)               // GET /api/v1/protected/chat/sse (SSE 流, 支持 Last-Event-ID 断点续传)
	chatProtected.POST("/messages", handler.MessageHandler)     // POST /api/v1/protected/chat/messages (通用消息发送: 私聊/群聊)
	chatProtected.POST("/sse/messages", handler.MessageHandler) // POST /api/v1/protected/chat/sse/messages (兼容别名)
	chatProtected.POST("/deliveries/:deliveryId/ack", handler.AcknowledgeDeliveryHandler)

	// 群 (应用级聊天室): 用户不能自建群/删群 (由应用/管理员创建), 可申请加入/退出聊天室
	chatProtected.POST("/groups/:groupId/join", handler.JoinGroupHandler)      // POST /api/v1/protected/chat/groups/:groupId/join (申请加入聊天室)
	chatProtected.POST("/groups/:groupId/leave", handler.LeaveGroupHandler)    // POST /api/v1/protected/chat/groups/:groupId/leave (退出聊天室)
	chatProtected.GET("/groups/mine", handler.MyGroupsHandler)                 // GET /api/v1/protected/chat/groups/mine (我的聊天室列表)
	chatProtected.GET("/groups/:groupId/members", handler.GroupMembersHandler) // GET /api/v1/protected/chat/groups/:groupId/members (聊天室成员列表, 需成员身份)
}
