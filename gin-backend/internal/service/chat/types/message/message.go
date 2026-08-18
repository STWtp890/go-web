// Package message 定义聊天消息类型体系 (传输无关, 供 websocket/sse 等复用)
//
// 分层设计:
//   - OriginMessageJson: Wire 层, 与 JSON 字节流一一对应的原始类型
//   - Message 接口 + PrivateMessage/GroupMessage: 总线层, 消息桥内部传递与持久化的载体
package message

import (
	"encoding/json"
	"fmt"
	"strings"

	"gin-backend/internal/service/chat/types/constant"
)

// MetaData 代表消息的元数据
type MetaData struct {
	DeliveryID  string `json:"deliveryId,omitempty"`
	MessageType string `json:"type"`      // 消息类型
	GroupType   string `json:"groupType"` // 群组类型
	From        string `json:"from"`      // 发送者标识
	To          string `json:"to"`        // 接收者或群 ID
	Timestamp   int64  `json:"timestamp"` // Unix 秒
}

// OriginMessageJson 从传输层 JSON 解析出的原始消息类型 (Wire Format)
// 与传输格式一一对应, 仅承载数据不承载业务逻辑。
// 由 Unmarshal 解析, 并经 FromOrigin 构造为具体消息类型。
type OriginMessageJson struct {
	MetaData    MetaData `json:"metadata"`
	ContentBody string   `json:"content"` // 消息内容体 (JSON 键保持 content, 与传输格式一致)
}

// Message 具体消息类型的统一接口, 是消息桥 (MessageBridge) 内部传递与持久化的载体
type Message interface {
	Type() string                // 消息类型: text / system / ack / error
	GroupType() string           // 群组类型: private / group
	From() string                // 发送者标识
	To() string                  // 接收者标识 或 群聊群 ID
	Timestamp() int64            // Unix 秒时间戳
	Content() string             // 消息内容
	IsPrivate() bool             // 是否私聊
	ToOrigin() OriginMessageJson // 转回 wire 层原始类型
	Marshal() []byte             // 序列化为 wire 格式 JSON (统一序列化入口)
}

// PrivateMessage 私聊消息 (GroupType = private)
type PrivateMessage struct {
	OriginMessageJson
}

// GroupMessage 群聊消息 (GroupType = group)
type GroupMessage struct {
	OriginMessageJson
}

// Type 返回消息类型
func (o OriginMessageJson) Type() string { return o.MetaData.MessageType }

// GroupType 返回群组类型
func (o OriginMessageJson) GroupType() string { return o.MetaData.GroupType }

// From 返回发送者标识
func (o OriginMessageJson) From() string { return o.MetaData.From }

// To 返回接收者标识或群聊房间 ID
func (o OriginMessageJson) To() string { return o.MetaData.To }

// Timestamp 返回 Unix 秒时间戳
func (o OriginMessageJson) Timestamp() int64 { return o.MetaData.Timestamp }

// Content 返回消息内容
func (o OriginMessageJson) Content() string { return o.ContentBody }

// IsPrivate 判断是否为私聊
func (o OriginMessageJson) IsPrivate() bool {
	return o.MetaData.GroupType == constant.GroupPrivate
}

// ToOrigin 返回 wire 层原始类型
func (o OriginMessageJson) ToOrigin() OriginMessageJson { return o }

// Marshal 序列化为 JSON 字节
func (o OriginMessageJson) Marshal() []byte {
	data, _ := json.Marshal(o)
	return data
}

// FromOrigin 根据 GroupType 将原始消息构造为具体 Message 类型
// 这是各传输层 (Unmarshal) 的解析入口
func FromOrigin(o OriginMessageJson) (Message, error) {
	switch o.MetaData.GroupType {
	case constant.GroupPrivate:
		return &PrivateMessage{OriginMessageJson: o}, nil
	case constant.GroupGroup:
		return &GroupMessage{OriginMessageJson: o}, nil
	default:
		return nil, fmt.Errorf("%w: %q", constant.ErrUnknownGroupType, o.MetaData.GroupType)
	}
}

// Unmarshal 从 wire 格式 JSON 反序列化并构造具体 Message 类型
func Unmarshal(raw []byte) (Message, error) {
	var origin OriginMessageJson
	if err := json.Unmarshal(raw, &origin); err != nil {
		return nil, err
	}
	if err := ValidateIncoming(origin); err != nil {
		return nil, err
	}
	return FromOrigin(origin)
}

const MaxContentBytes = 48 * 1024

// ValidateIncoming 校验客户端可提交的最小消息契约；From、Timestamp 由服务端覆盖。
func ValidateIncoming(o OriginMessageJson) error {
	if o.MetaData.MessageType != constant.TypeText {
		return fmt.Errorf("不支持的消息类型")
	}
	if strings.TrimSpace(o.MetaData.To) == "" {
		return fmt.Errorf("接收者不能为空")
	}
	if strings.TrimSpace(o.ContentBody) == "" || len(o.ContentBody) > MaxContentBytes {
		return fmt.Errorf("消息内容不能为空或过长")
	}
	if o.MetaData.GroupType != constant.GroupPrivate && o.MetaData.GroupType != constant.GroupGroup {
		return fmt.Errorf("%w: %q", constant.ErrUnknownGroupType, o.MetaData.GroupType)
	}
	return nil
}
