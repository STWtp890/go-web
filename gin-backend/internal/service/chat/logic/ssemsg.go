package logic

import (
	"context"
	"errors"

	"gin-backend/internal/service/chat"
	msg "gin-backend/internal/service/chat/types/message"
)

// SendMessageLogic 处理客户端经 HTTP POST 提交的消息
// 解析为公共 message 类型后, 由服务端强制覆盖 From 字段 (防止伪造身份),
// 再经 Hub.Publish 直达投递 (私聊/群聊)
func SendMessageLogic(ctx context.Context, subject string, raw []byte) error {
	h := chat.SSEHub()
	if h == nil {
		return errors.New("SSE Hub 未初始化")
	}

	m, err := msg.Unmarshal(raw)
	if err != nil {
		return err
	}
	origin := m.ToOrigin()
	origin.MetaData.From = subject // 服务端强制发送者身份
	built, err := msg.FromOrigin(origin)
	if err != nil {
		return err
	}
	h.Publish(ctx, built)
	return nil
}
