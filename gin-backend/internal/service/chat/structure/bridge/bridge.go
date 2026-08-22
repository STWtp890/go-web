// Package bridge 定义聊天消息桥: 统一维护连接注册与投递 (私聊直投 + 群聊最近消息窗口)
// 离线推送: 私聊离线落库 (MessageStore), 接入后异步补发; 群成员/群历史经 GroupStore 懒加载恢复
package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"gin-backend/internal/service/chat/store"
	"gin-backend/internal/service/chat/types/client"
	wsclient "gin-backend/internal/service/chat/types/client/websocket"
	"gin-backend/internal/service/chat/types/constant"
	"gin-backend/internal/service/chat/types/group"
	"gin-backend/internal/service/chat/types/message"

	"github.com/gorilla/websocket"
)

var ErrNotGroupMember = errors.New("chat: 非群成员")

// MessageBridge 消息桥: 统一维护连接注册 (userClients) 与投递 (私聊直投 / 群聊窗口)
// 持久化: messages (私聊离线/群历史) + groupMgr (群管理, DB 为成员真相源), 均懒加载接入;
// store 为 nil 或 DB 不可用时降级为纯内存模式 (消息仅在线投递, 不落库不补发)
type MessageBridge struct {
	userClients *UserClientMap
	groupMgr    *group.Manager // 群: 状态映射 + 群管理 + 懒加载
	messages    store.MessageStore
}

// NewMessageBridge 创建消息桥
// :Param
// - `messages` 消息持久化 (可为 nil, 降级纯内存)
// - `groupsStore` 群组持久化 (可为 nil, 降级纯内存)
func NewMessageBridge(messages store.MessageStore, groupsStore store.GroupStore) *MessageBridge {
	return &MessageBridge{
		userClients: NewUserClientMap(),
		groupMgr:    group.NewManager(groupsStore, messages),
		messages:    messages,
	}
}

// NewWebSocketChannel 创建 WebSocket 用户通道并注册 (Bridge 提供接口, 创建即注册)
// 接入后异步补发: 私聊离线消息 + 群成员/群历史懒加载 (不阻塞连接建立)
func (b *MessageBridge) NewWebSocketChannel(ctx context.Context, subject, sessionID string, conn *websocket.Conn) *client.UserChannel {
	inCh := make(chan []byte, 64)
	outCh := make(chan []byte, 64)
	errCh := make(chan error, 4)

	ws := wsclient.NewWebSocketClient(ctx, conn, inCh)
	uc := client.NewUserChannel(subject, sessionID, ws)
	// 心跳停止 (连接异常/超时/主动关闭) → 触发连接清理 (Detach → Offline → Close)
	ws.SetOnStop(func() { b.Detach(subject, uc) })
	ws.Init(inCh, outCh, errCh)

	// 入站消费: 反序列化 + 覆盖服务端可信字段 + 回投 Publish。
	go func() {
		for data := range inCh {
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
			deliveries, err := b.Publish(ctx, built)
			if err != nil {
				uc.Push(controlMessage(origin, subject, constant.TypeError, map[string]any{
					"message": "消息发送失败",
				}))
				continue
			}
			deliveryIDs := make([]string, 0, len(deliveries))
			for _, delivery := range deliveries {
				deliveryIDs = append(deliveryIDs, delivery.DeliveryID)
			}
			uc.Push(controlMessage(origin, subject, constant.TypeAck, map[string]any{
				"deliveryIds": deliveryIDs,
			}))
		}
	}()

	uc.Online()
	b.userClients.Attach(subject, uc)

	// 接入后异步补发 (懒加载接入): 私聊离线 + 用户所属群
	go b.flushOffline(ctx, subject, uc)
	go b.joinUserGroups(ctx, subject, uc)
	return uc
}

// controlMessage 构造不经持久化和业务投递链路的 WebSocket 控制帧。
// ClientMessageID 让浏览器可以将 ACK/错误与本地消息精确关联。
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

// Attach 注册用户通道 (委托)
func (b *MessageBridge) Attach(subject string, uc *client.UserChannel) {
	b.userClients.Attach(subject, uc)
}

// Detach 移除用户通道并下线 (委托)
func (b *MessageBridge) Detach(subject string, uc *client.UserChannel) {
	b.userClients.Detach(subject, uc)
	uc.Offline()
}

// Get 获取用户通道 (委托)
func (b *MessageBridge) Get(subject string) *client.UserChannel {
	return b.userClients.Get(subject)
}

// RevokeSession 关闭当前实例中 subject 对应且 sid 匹配的聊天连接。
func (b *MessageBridge) RevokeSession(subject, sessionID string) int {
	return b.userClients.RevokeSession(subject, sessionID)
}

// Publish 消息投递入口: 私聊直投 / 群聊窗口广播
func (b *MessageBridge) Publish(ctx context.Context, m message.Message) ([]store.Delivery, error) {
	switch m.GroupType() {
	case constant.GroupGroup:
		return b.deliverToGroup(ctx, m)
	default: // GroupPrivate
		return b.deliverToUser(ctx, m)
	}
}

func (b *MessageBridge) Acknowledge(ctx context.Context, subject, deliveryID string) (bool, error) {
	ds, ok := b.messages.(store.DeliveryStore)
	if !ok {
		return false, errors.New("chat: 可靠投递存储未初始化")
	}
	return ds.Acknowledge(ctx, subject, deliveryID)
}

// JoinGroup 加入聊天室 (委托群管理器, 先写 DB 后入内存原子, 返回窗口消息供补发)
func (b *MessageBridge) JoinGroup(ctx context.Context, groupID, memberID string) ([]message.Message, error) {
	if b.groupMgr == nil {
		return nil, group.ErrGroupServiceUnavailable
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
