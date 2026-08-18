package logic

import (
	"context"
	"log/slog"

	"gin-backend/internal/common/service/jwt"
	"gin-backend/internal/service/chat"

	"github.com/gorilla/websocket"
)

// WebSocketLogic 处理 WebSocket 连接的全生命周期
// 经 Hub 的 NewWebSocketChannel 创建并注册 (Bridge 提供接口, 创建即注册)
func WebSocketLogic(ctx context.Context, subject, sessionID string, conn *websocket.Conn) {
	h := chat.WebSocketHub()
	if h == nil {
		return
	}

	uc := h.NewWebSocketChannel(ctx, subject, sessionID, conn)
	if uc == nil {
		return
	}
	defer h.Detach(subject, uc)
	if !sessionStillActive(ctx, subject, sessionID) {
		h.Detach(subject, uc)
		return
	}

	// 阻塞直到客户端断开或会话撤销主动关闭连接。
	select {
	case <-ctx.Done():
	case <-uc.Done():
	}
}

func sessionStillActive(ctx context.Context, subject, sessionID string) bool {
	active, err := jwt.IsUserSessionActive(ctx, subject, sessionID)
	if err != nil {
		slog.Warn("chat_session_recheck_failed", slog.String("subject", subject), slog.String("error", err.Error()))
		return false
	}
	return active
}
