package hub

import (
	"context"
	"fmt"

	"gin-backend/internal/service/chat/store"
	"gin-backend/internal/service/chat/structure/bridge"
	"gin-backend/internal/service/chat/types/client"

	msg "gin-backend/internal/service/chat/types/message"

	"github.com/gorilla/websocket"
)

// Hub 聊天连接入口薄壳: 注入维护 MessageBridge
// WebSocket 连接创建由 Bridge 提供接口, Hub 侧调用; 生命周期与投递委托 bridge
type Hub struct {
	bridge *bridge.MessageBridge
}

// NewHub 创建 Hub 并注入消息桥
func NewHub(b *bridge.MessageBridge) *Hub {
	return &Hub{bridge: b}
}

// Bridge 返回注入的消息桥
func (h *Hub) Bridge() *bridge.MessageBridge {
	return h.bridge
}

// NewWebSocketChannel 创建 WebSocket 用户通道并注册 (Bridge 提供接口, Hub 侧调用)
func (h *Hub) NewWebSocketChannel(ctx context.Context, subject, sessionID string, conn *websocket.Conn) *client.UserChannel {
	if h.bridge == nil {
		return nil
	}
	return h.bridge.NewWebSocketChannel(ctx, subject, sessionID, conn)
}

// Publish 入站消息投递入口 (委托 bridge)
func (h *Hub) Publish(ctx context.Context, m msg.Message) ([]store.Delivery, error) {
	if h.bridge != nil {
		return h.bridge.Publish(ctx, m)
	}
	return nil, fmt.Errorf("chat bridge 未初始化")
}

// Attach 注册用户通道 (委托 bridge)
func (h *Hub) Attach(subject string, uc *client.UserChannel) {
	if h.bridge != nil {
		h.bridge.Attach(subject, uc)
	}
}

// Detach 移除用户通道 (委托 bridge)
func (h *Hub) Detach(subject string, uc *client.UserChannel) {
	if h.bridge != nil {
		h.bridge.Detach(subject, uc)
	}
}

// RevokeSession 关闭此 Hub 所在实例中匹配会话的连接。
func (h *Hub) RevokeSession(subject, sessionID string) int {
	if h.bridge == nil {
		return 0
	}
	return h.bridge.RevokeSession(subject, sessionID)
}
