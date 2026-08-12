package logic

import (
	"context"
	"net/http"

	"gin-backend/internal/service/chat"
)

// SSELogic 处理 SSE 连接的全生命周期
// 经 Hub 的 NewSSEChannel 创建并注册 (Last-Event-ID 断点续传已暂时搁置)
func SSELogic(ctx context.Context, subject string, lastEventID string, w http.ResponseWriter) {
	h := chat.SSEHub()
	if h == nil {
		return
	}

	uc := h.NewSSEChannel(ctx, subject, w)
	if uc == nil {
		return
	}
	defer h.Detach(subject, uc)

	// 阻塞直到连接断开或服务端关闭
	<-ctx.Done()
}
