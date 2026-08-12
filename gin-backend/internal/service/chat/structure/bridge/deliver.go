// 消息投递: 私聊直投/离线落库, 群聊落库+窗口+在线成员 (MessageBridge 内部方法)
package bridge

import (
	"context"
	"log/slog"

	"gin-backend/internal/service/chat/types/message"
)

// deliverToUser 私聊: 在线直投; 离线落库 (待上线补发, 懒加载接入)
func (b *MessageBridge) deliverToUser(ctx context.Context, m message.Message) {
	if uc := b.userClients.Get(m.To()); uc != nil {
		uc.Push(m)
		return
	}
	if b.messages != nil {
		if err := b.messages.Save(ctx, m); err != nil {
			slog.Warn("离线消息落库失败", slog.String("to", m.To()), slog.String("error", err.Error()))
		}
	}
}

// deliverToGroup 群聊: 发送前校验"在群"身份 (内存查询) → 落库群历史 + 入窗口 + 在线成员投递
// 权限模型: 群消息发送需"在群" (群成员, 隐含在库); 群模型启动初始化到内存 (EnsureAllLoaded)
// 非成员/群不存在直接拒绝 (防越权)
func (b *MessageBridge) deliverToGroup(ctx context.Context, m message.Message) {
	// 发送前校验: 仅群成员可发 (内存查询); 群不存在/非成员均拒绝
	if b.groupMgr == nil || !b.groupMgr.IsMember(m.To(), m.From()) {
		slog.Warn("群消息发送被拒绝: 非群成员或群不存在",
			slog.String("group", m.To()), slog.String("from", m.From()))
		return
	}
	if b.messages != nil {
		if err := b.messages.Save(ctx, m); err != nil {
			slog.Warn("群消息落库失败", slog.String("group", m.To()), slog.String("error", err.Error()))
		}
	}
	s := b.groupMgr.Groups().Router(m.To())
	if !s.Loaded() {
		b.groupMgr.EnsureLoaded(ctx, m.To(), s)
	}
	s.Push(m)
	for _, subject := range s.Members() {
		if uc := b.userClients.Get(subject); uc != nil {
			uc.Push(m)
		}
	}
}
