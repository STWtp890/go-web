// Package chat 聊天业务模块 (业务级初始化)
package chat

import (
	"context"
	"fmt"
	"log/slog"

	"gin-backend/internal/common/service/sessionevent"
	"gin-backend/internal/config"
	"gin-backend/internal/service/chat/store"
	"gin-backend/internal/service/chat/structure/bridge"
)

// 包级聊天服务实例，由 Init 统一组装并管理生命周期。
var chatService *bridge.MessageBridge

// Init 初始化聊天服务、持久化依赖与会话撤销订阅。
// 遵循 service 层统一约定: 函数名 Init, 返回清理函数 (无清理需求时返回 nil)
// :Param
// - `conf` 配置文件
// :Return
// - `func()` 清理函数, 由 app 的运行时生命周期在退出时调用
func Init(conf *config.Config) func() {
	// 单一 WebSocket 消息桥。
	// 持久化懒加载接入: store 首次使用时才获取 PostgreSQL 连接 (复用 ServiceMarkdown),
	// DB 不可用时桥自动降级为纯内存模式 (仅在线投递, 不落库不补发)
	chatService = bridge.NewMessageBridge(
		store.NewTimescaleMessageStore(),
		store.NewGormGroupStore(),
	)
	// 应用启动初始化: 群模型 (群+成员) 从 DB 加载到内存
	// 之后群消息发送/成员鉴权直接查内存; 新增成员先写 DB 后入内存 (原子, 由 Manager 保证)
	chatService.EnsureAllGroupsLoaded(context.Background())

	listenerCtx, cancelListener := context.WithCancel(context.Background())
	subscription, err := sessionevent.Subscribe(listenerCtx, "chat-session-revoker", HandleSessionRevoked)
	if err != nil {
		cancelListener()
		chatService = nil
		panic(fmt.Sprintf("订阅会话撤销事件失败: %v", err))
	}

	return func() {
		_ = subscription.Close()
		cancelListener()
		chatService = nil
	}
}

// Service 返回当前聊天服务实例；Init 未完成或清理后返回 nil。
func Service() *bridge.MessageBridge { return chatService }

// HandleSessionRevoked 关闭本应用实例中与撤销事件精确匹配的聊天连接。
func HandleSessionRevoked(_ context.Context, event sessionevent.SessionRevokedEvent) error {
	if event.Type != sessionevent.EventSessionRevoked || event.PrincipalType != sessionevent.PrincipalUser {
		return nil
	}

	service := Service()
	if service == nil {
		return nil
	}

	closed := service.RevokeSession(event.PrincipalID, event.SessionID)
	slog.Info("chat_session_revoked",
		slog.String("event_id", event.EventID),
		slog.String("user_id", event.PrincipalID),
		slog.String("sid", event.SessionID),
		slog.String("reason", string(event.Reason)),
		slog.Int("closed_connections", closed),
	)
	return nil
}
