// chat 实体缓存对象
package cache

import "gin-backend/internal/model/orm/chat"

// MessageCache 聊天消息缓存对象
type MessageCache struct {
	ID        uint   `json:"id"`
	GroupType string `json:"group_type"`
	FromID    string `json:"from_id"`
	ToID      string `json:"to_id"`
	Payload   string `json:"payload"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

// FromMessage 由 ORM 聊天消息模型构造缓存对象
func FromMessage(m *chat.Message) *MessageCache {
	return &MessageCache{
		ID:        m.ID,
		GroupType: m.GroupType,
		FromID:    m.FromID,
		ToID:      m.ToID,
		Payload:   m.Payload,
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
	}
}
