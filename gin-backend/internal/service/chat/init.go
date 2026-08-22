// Package chat 聊天业务模块 (业务级初始化)
package chat

import (
	"context"
	"fmt"

	"gin-backend/internal/common/service/sessionevent"
	"gin-backend/internal/config"
	"gin-backend/internal/service/chat/store"
	"gin-backend/internal/service/chat/structure/bridge"
	hub "gin-backend/internal/service/chat/structure/hub"
)

// 包级 Hub 实例 (由 Init 创建, 经 Getter 暴露给 logic 层)
var chatHub *hub.Hub

// Init chat 业务级初始化: 创建单一 WebSocket Hub。
// 遵循 service 层统一约定: 函数名 Init, 返回清理函数 (无清理需求时返回 nil)
// :Param
// - `conf` 配置文件
// :Return
// - `func()` 清理函数, 由 service.Init 在退出时调用
func Init(conf *config.Config) func() {
	// 单一 WebSocket 消息桥。
	// 持久化懒加载接入: store 首次使用时才获取 PostgreSQL 连接 (复用 ServiceMarkdown),
	// DB 不可用时桥自动降级为纯内存模式 (仅在线投递, 不落库不补发)
	shared := bridge.NewMessageBridge(
		store.NewTimescaleMessageStore(),
		store.NewGormGroupStore(),
	)
	// 应用启动初始化: 群模型 (群+成员) 从 DB 加载到内存
	// 之后群消息发送/成员鉴权直接查内存; 新增成员先写 DB 后入内存 (原子, 由 Manager 保证)
	shared.EnsureAllGroupsLoaded(context.Background())
	chatHub = hub.NewHub(shared)

	listenerCtx, cancelListener := context.WithCancel(context.Background())
	subscription, err := sessionevent.Subscribe(listenerCtx, "chat-session-revoker", HandleSessionRevoked)
	if err != nil {
		cancelListener()
		chatHub = nil
		panic(fmt.Sprintf("订阅会话撤销事件失败: %v", err))
	}

	return func() {
		_ = subscription.Close()
		cancelListener()
		chatHub = nil
	}
}

// Hub 返回全局 WebSocket Hub 实例 (Init 未调用时为 nil)。
func Hub() *hub.Hub { return chatHub }
