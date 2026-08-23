package logic

import (
	"context"
	"log/slog"

	"gin-backend/internal/common/service/jwt"
	"gin-backend/internal/service/chat"
	"gin-backend/internal/service/chat/types/client"
)

// WebSocketLogic 处理 WebSocket 连接的全生命周期
// 经聊天服务创建并注册用户通道。
func WebSocketLogic(ctx context.Context, subject, sessionID string, conn client.Connection) {
	service := chat.Service()
	if service == nil {
		_ = conn.Close()
		return
	}

	// 在注册连接和启动 pending 重放前再次确认会话，缩小鉴权与协议升级之间的竞态窗口。
	if !sessionStillActive(ctx, subject, sessionID) {
		_ = conn.Close()
		return
	}

	uc := service.OpenConnection(ctx, subject, sessionID, conn)
	if uc == nil {
		_ = conn.Close()
		return
	}
	
	defer service.Detach(subject, uc)
	if !sessionStillActive(ctx, subject, sessionID) {
		service.Detach(subject, uc)
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
