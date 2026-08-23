// 消息投递: 私聊直投/离线落库, 群聊落库+窗口+在线成员 (MessageBridge 内部方法)
package bridge

import (
	"context"
	"errors"
	"log/slog"

	"gin-backend/internal/service/chat/store"
	"gin-backend/internal/service/chat/types/message"
)

// deliverToUser 私聊: 在线直投; 离线落库 (待上线补发, 懒加载接入)
func (b *MessageBridge) deliverToUser(ctx context.Context, m message.Message) ([]store.Delivery, error) {
	deliveries, err := b.persist(ctx, m, []string{m.To()})
	if err != nil {
		return nil, err
	}
	if uc := b.userClients.Get(m.To()); uc != nil {
		if !uc.Push(deliveries[0].Message) {
			b.Detach(m.To(), uc)
		}
	}
	return deliveries, nil
}

// deliverToGroup 群聊: 发送前校验"在群"身份 (内存查询) → 落库群历史 + 入窗口 + 在线成员投递
// 权限模型: 群消息发送需"在群" (群成员, 隐含在库); 群模型启动初始化到内存 (EnsureAllLoaded)
// 非成员/群不存在直接拒绝 (防越权)
func (b *MessageBridge) deliverToGroup(ctx context.Context, m message.Message) ([]store.Delivery, error) {
	// 群成员发送校验
	if b.groupMgr == nil || !b.groupMgr.IsMember(m.To(), m.From()) {
		slog.Warn("群消息发送被拒绝: 非群成员或群不存在",
			slog.String("group", m.To()), slog.String("from", m.From()))
		return nil, ErrNotGroupMember
	}

	s := b.groupMgr.Groups().Router(m.To())
	if !s.Loaded() {
		b.groupMgr.EnsureLoaded(ctx, m.To(), s)
	}
	members := s.Members()
	deliveries, err := b.persist(ctx, m, members)
	if err != nil {
		return nil, err
	}
	s.Push(m)
	for _, delivery := range deliveries {
		if uc := b.userClients.Get(delivery.RecipientID); uc != nil {
			if !uc.Push(delivery.Message) {
				b.Detach(delivery.RecipientID, uc)
			}
		}
	}
	return deliveries, nil
}

func (b *MessageBridge) persist(ctx context.Context, m message.Message, recipients []string) ([]store.Delivery, error) {
	ds, ok := b.messages.(store.DeliveryStore)
	if !ok {
		return nil, errors.New("chat: 可靠投递存储未初始化")
	}
	return ds.SaveWithDeliveries(ctx, m, recipients)
}
