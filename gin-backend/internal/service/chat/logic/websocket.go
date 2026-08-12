package logic

import (
	"context"

	"gin-backend/internal/service/chat"

	"github.com/gorilla/websocket"
)

// WebSocketLogic 处理 WebSocket 连接的全生命周期
// 经 Hub 的 NewWebSocketChannel 创建并注册 (Bridge 提供接口, 创建即注册)
func WebSocketLogic(ctx context.Context, subject string, conn *websocket.Conn) {
	h := chat.WebSocketHub()
	if h == nil {
		return
	}

	uc := h.NewWebSocketChannel(ctx, subject, conn)
	if uc == nil {
		return
	}
	defer h.Detach(subject, uc)

	// 阻塞直到连接上下文取消 (断开/关闭)
	<-ctx.Done()
}
