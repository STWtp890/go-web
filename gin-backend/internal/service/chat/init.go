// Package chat 聊天业务模块 (业务级初始化)
package chat

import (
	"context"

	"gin-backend/internal/config"
	"gin-backend/internal/service/chat/store"
	"gin-backend/internal/service/chat/structure/bridge"
	hub "gin-backend/internal/service/chat/structure/hub"
)

// 包级 Hub 实例 (由 Init 创建, 经 Getter 暴露给 logic 层)
var (
	wsHub  *hub.Hub
	sseHub *hub.Hub
)

// Init chat 业务级初始化: 创建 WebSocket / SSE Hub, 共享单一 MessageBridge
// 连接注册与投递统一由 bridge 维护, WS/SSE 同表 → 消息互通
// 遵循 service 层统一约定: 函数名 Init, 返回清理函数 (无清理需求时返回 nil)
// :Param
// - `conf` 配置文件
// :Return
// - `func()` 清理函数, 由 service.Init 在退出时调用
func Init(conf *config.Config) func() {
	// 单一共享消息桥 (WS/SSE 同表, 消息互通)
	// 持久化懒加载接入: store 首次使用时才获取 PostgreSQL 连接 (复用 ServiceMarkdown),
	// DB 不可用时桥自动降级为纯内存模式 (仅在线投递, 不落库不补发)
	shared := bridge.NewMessageBridge(
		store.NewGormMessageStore(),
		store.NewGormGroupStore(),
	)
	// 应用启动初始化: 群模型 (群+成员) 从 DB 加载到内存
	// 之后群消息发送/成员鉴权直接查内存; 新增成员先写 DB 后入内存 (原子, 由 Manager 保证)
	shared.EnsureAllGroupsLoaded(context.Background())
	wsHub = hub.NewHub(shared)
	sseHub = hub.NewHub(shared)

	return func() {
		wsHub = nil
		sseHub = nil
	}
}

// WebSocketHub 返回全局 WebSocket Hub 实例 (Init 未调用时为 nil)
func WebSocketHub() *hub.Hub { return wsHub }

// SSEHub 返回全局 SSE Hub 实例 (Init 未调用时为 nil)
func SSEHub() *hub.Hub { return sseHub }
