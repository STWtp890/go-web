// 聊天消息持久化模型
package chat

import "gin-backend/internal/model/orm"

// Message 聊天消息持久化模型 (历史 / 离线)
// :Field
// - GroupType: 群组类型 (private / group)
// - FromID: 发送者标识 (JWT sub)
// - ToID: 私聊为接收者 subject, 群聊为群 ID (索引支撑离线拉取/群历史)
// - Payload: OriginMessageJson JSON 序列化 (按需还原 message.Message)
type Message struct {
	ID        uint   `json:"id" gorm:"primaryKey;autoIncrement"`
	GroupType string `json:"group_type" gorm:"size:16;not null"`
	FromID    string `json:"from_id" gorm:"size:64;index;not null"`
	ToID      string `json:"to_id" gorm:"size:64;index:idx_message_to_type,priority:1;not null"`
	Payload   string `json:"-" gorm:"type:text;not null"`
	orm.TimeFiled
}

// MessageDelivery 是一名接收者对一条消息的可靠投递状态。
// message_id + message_created_at 指向 TimescaleDB hypertable 中的消息行。
type MessageDelivery struct {
	DeliveryID       string `json:"delivery_id" gorm:"primaryKey;size:36"`
	MessageID        uint   `json:"message_id" gorm:"not null;index"`
	MessageCreatedAt int64  `json:"message_created_at" gorm:"not null;index"`
	RecipientID      string `json:"recipient_id" gorm:"size:64;not null;index:idx_delivery_pending,priority:1"`
	CreatedAt        int64  `json:"created_at" gorm:"autoCreateTime:true;index:idx_delivery_pending,priority:2"`
}

func (*MessageDelivery) TableName() string { return "chat_message_deliveries" }

// TableName 指定表名
func (*Message) TableName() string {
	return "chat_messages"
}
