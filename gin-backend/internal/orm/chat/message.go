// 聊天消息持久化模型
package chat

import "gin-backend/internal/orm"

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

// TableName 指定表名
func (*Message) TableName() string {
	return "chat_messages"
}
