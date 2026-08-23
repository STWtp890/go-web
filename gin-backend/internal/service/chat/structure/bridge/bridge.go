// Package bridge 定义聊天消息桥: 统一维护连接注册、持久化投递与 pending 重放。
package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"gin-backend/internal/service/chat/store"
	"gin-backend/internal/service/chat/types/client"
	"gin-backend/internal/service/chat/types/constant"
	"gin-backend/internal/service/chat/types/group"
	"gin-backend/internal/service/chat/types/message"
)

var ErrNotGroupMember = errors.New("chat: 非群成员")

// MessageBridge 消息桥: 统一维护连接注册、私聊/群聊投递和 pending delivery 重放。
// messages 是可靠投递真相源；groupMgr 以 DB 为群成员关系真相源。
type MessageBridge struct {
	userClients *UserClientMap
	groupMgr    *group.Manager // 群: 状态映射 + 群管理 + 懒加载
	messages    store.MessageStore
}

// NewMessageBridge 创建消息桥
// :Param
// - `messages` 消息与 pending delivery 持久化
// - `groupsStore` 群组持久化 (可为 nil, 降级纯内存)
func NewMessageBridge(messages store.MessageStore, groupsStore store.GroupStore) *MessageBridge {
	return &MessageBridge{
		userClients: NewUserClientMap(),
		groupMgr:    group.NewManager(groupsStore, messages),
		messages:    messages,
	}
}

// OpenConnection 注册一条已适配的双向连接，并启动入站消费与 pending 重放。
// MessageBridge 只依赖连接接口，不感知 Gorilla WebSocket 等具体传输实现。
func (b *MessageBridge) OpenConnection(ctx context.Context, subject, sessionID string, conn client.Connection) *client.UserChannel {
	if conn == nil {
		return nil
	}
	uc := client.NewUserChannel(subject, sessionID, conn)
	uc.BeginReplay()
	uc.Online()
	b.userClients.Attach(subject, uc)
	// 读写错误、心跳失败、上下文取消或主动关闭均汇聚到同一条清理路径。
	conn.Start(func() { b.Detach(subject, uc) })

	// 入站消费: 反序列化 + 覆盖服务端可信字段 + 回投 Publish。
	go func() {
		for data := range conn.Incoming() {
			m, err := message.Unmarshal(data)
			if err != nil {
				continue
			}
			origin := m.ToOrigin()
			origin.MetaData.From = subject
			origin.MetaData.Timestamp = time.Now().Unix()
			built, err := message.FromOrigin(origin)
			if err != nil {
				continue
			}
			deliveries, err := b.publish(ctx, built)
			if err != nil {
				if !uc.Push(controlMessage(origin, subject, constant.TypeError, map[string]any{
					"message": "消息发送失败",
				})) {
					b.Detach(subject, uc)
					return
				}
				continue
			}
			if !uc.Push(controlMessage(origin, subject, constant.TypeAccepted, map[string]any{
				"messageId": deliveries[0].MessageID,
			})) {
				b.Detach(subject, uc)
				return
			}
		}
	}()

	// 接入后从唯一 pending-delivery 真相源异步重放私聊与群聊。
	go b.replayPending(ctx, subject, uc)
	return uc
}

// controlMessage 构造不经持久化和业务投递链路的 WebSocket 控制帧。
// ClientMessageID 让浏览器可以将 accepted/error 与本地消息精确关联。
func controlMessage(request message.OriginMessageJson, subject, messageType string, payload any) message.Message {
	data, _ := json.Marshal(payload)
	control, _ := message.FromOrigin(message.OriginMessageJson{
		MetaData: message.MetaData{
			ClientMessageID: request.MetaData.ClientMessageID,
			MessageType:     messageType,
			GroupType:       request.MetaData.GroupType,
			From:            "server",
			To:              subject,
			Timestamp:       time.Now().Unix(),
		},
		ContentBody: string(data),
	})
	return control
}

// Detach 移除用户通道并下线 (委托)
func (b *MessageBridge) Detach(subject string, uc *client.UserChannel) {
	b.userClients.Detach(subject, uc)
	uc.Offline()
}

// RevokeSession 关闭当前实例中 subject 对应且 sid 匹配的聊天连接。
func (b *MessageBridge) RevokeSession(subject, sessionID string) int {
	return b.userClients.RevokeSession(subject, sessionID)
}

// publish 是消息投递的内部入口：根据会话类型选择私聊直投或群聊广播。
func (b *MessageBridge) publish(ctx context.Context, m message.Message) ([]store.Delivery, error) {
	switch m.GroupType() {
	case constant.GroupGroup:
		return b.deliverToGroup(ctx, m)
	default: // GroupPrivate
		return b.deliverToUser(ctx, m)
	}
}

func (b *MessageBridge) Ack(ctx context.Context, subject string, deliveryIDs []string) error {
	ds, ok := b.messages.(store.DeliveryStore)
	if !ok {
		return errors.New("chat: 可靠投递存储未初始化")
	}
	return ds.Ack(ctx, subject, deliveryIDs)
}

// JoinGroup 加入聊天室 (委托群管理器, 先写 DB 后入内存原子)
func (b *MessageBridge) JoinGroup(ctx context.Context, groupID, memberID string) error {
	if b.groupMgr == nil {
		return group.ErrGroupServiceUnavailable
	}
	return b.groupMgr.JoinGroup(ctx, groupID, memberID)
}

// LeaveGroup 退出聊天室 (委托群管理器)
func (b *MessageBridge) LeaveGroup(ctx context.Context, groupID, memberID string) error {
	if b.groupMgr == nil {
		return group.ErrGroupServiceUnavailable
	}
	return b.groupMgr.LeaveGroup(ctx, groupID, memberID)
}

// EnsureAllGroupsLoaded 应用启动初始化: 群模型 (群+成员) 从 DB 加载到内存
// 之后群消息发送/成员鉴权均查内存 (DB 为成员关系真相源)
func (b *MessageBridge) EnsureAllGroupsLoaded(ctx context.Context) {
	if b.groupMgr == nil {
		return
	}
	b.groupMgr.EnsureAllLoaded(ctx)
}

// IsGroupMember 判断用户是否群成员 (内存查询, 发送前/查看前鉴权)
func (b *MessageBridge) IsGroupMember(groupID, subject string) bool {
	if b.groupMgr == nil {
		return false
	}
	return b.groupMgr.IsMember(groupID, subject)
}

// GroupMembers 群成员列表 (委托群管理器)
func (b *MessageBridge) GroupMembers(ctx context.Context, groupID string) ([]string, error) {
	if b.groupMgr == nil {
		return nil, group.ErrGroupServiceUnavailable
	}
	return b.groupMgr.GroupMembers(ctx, groupID)
}

// MyGroups 我的群列表 (委托群管理器)
func (b *MessageBridge) MyGroups(ctx context.Context, memberID string) ([]string, error) {
	if b.groupMgr == nil {
		return nil, group.ErrGroupServiceUnavailable
	}
	return b.groupMgr.MyGroups(ctx, memberID)
}
