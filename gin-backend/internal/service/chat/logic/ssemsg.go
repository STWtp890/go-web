package logic

import (
	"context"
	"errors"
	"time"

	"gin-backend/internal/service/chat"
	msg "gin-backend/internal/service/chat/types/message"
)

// SendMessageLogic 处理客户端经 HTTP POST 提交的消息
// 解析为公共 message 类型后, 由服务端强制覆盖 From 字段 (防止伪造身份),
// 再经 Hub.Publish 直达投递 (私聊/群聊)
func SendMessageLogic(ctx context.Context, subject string, raw []byte) ([]string, error) {
	h := chat.SSEHub()
	if h == nil {
		return nil, errors.New("SSE Hub 未初始化")
	}

	m, err := msg.Unmarshal(raw)
	if err != nil {
		return nil, err
	}
	origin := m.ToOrigin()
	origin.MetaData.From = subject // 服务端强制发送者身份
	origin.MetaData.Timestamp = time.Now().Unix()
	built, err := msg.FromOrigin(origin)
	if err != nil {
		return nil, err
	}
	deliveries, err := h.Publish(ctx, built)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(deliveries))
	for _, d := range deliveries {
		ids = append(ids, d.DeliveryID)
	}
	return ids, nil
}
