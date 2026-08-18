package logic

import (
	"context"
	"net/http"

	"gin-backend/internal/service/chat"
)

// SSELogic 处理 SSE 连接的全生命周期
// 经 Hub 的 NewSSEChannel 创建并注册 (Last-Event-ID 断点续传已暂时搁置)
func SSELogic(ctx context.Context, subject, sessionID, lastEventID string, w http.ResponseWriter) {
	h := chat.SSEHub()
	if h == nil {
		return
	}

	uc := h.NewSSEChannel(ctx, subject, sessionID, lastEventID, w)
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
