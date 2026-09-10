// 聊天消息持久化: MessageStore 接口 + gorm 实现
package store

import (
	"context"

	ormchat "gin-backend/internal/model/orm/chat"
	"gin-backend/internal/service/chat/types/constant"
	"gin-backend/internal/service/chat/types/message"
)

// MessageStore 消息持久化接口 (私聊离线 / 群聊历史)
// :Method
// - `Save`: 落库一条消息, Payload 存 wire 格式 (Message.Marshal())
// - `FetchOffline`: 拉取私聊离线未读消息 (to_id = 接收者)
// - `FetchGroupHistory`: 拉取群聊历史 (to_id = 群 ID, 最新 limit 条时序正序)
// - `DeleteByIDs`: 标记已投递消息软删除 (物理清理由 sidecar 定时执行)
type MessageStore interface {
	Save(ctx context.Context, m message.Message) error
	FetchOffline(ctx context.Context, toID string, limit int) ([]message.TimeScaleMessage, error)
	FetchGroupHistory(ctx context.Context, groupID string, limit int) ([]message.TimeScaleMessage, error)
	DeleteByIDs(ctx context.Context, ids []uint) error
}

// GormMessageStore MessageStore 的 gorm 实现 (PostgreSQL, DB 连接懒加载见 gormStore)
type GormMessageStore struct {
	gormStore
}

// NewGormMessageStore 创建 gorm 消息存储 (无参, DB 连接懒加载)
func NewGormMessageStore() *GormMessageStore {
	return &GormMessageStore{}
}

// Save 落库一条消息 (Payload 为 wire 格式 JSON, 读取时经 message.Unmarshal 还原)
func (s *GormMessageStore) Save(ctx context.Context, m message.Message) error {
	db, err := s.dbConn()
	if err != nil {
		return err
	}
	row := ormchat.Message{
		GroupType: m.GroupType(),
		FromID:    m.From(),
		ToID:      m.To(),
		Payload:   string(m.Marshal()),
	}
	return db.WithContext(ctx).Create(&row).Error
}

// FetchOffline 拉取私聊离线未读消息 (to_id = 接收者), 按 ID 升序保证时序
// gorm 软删自动附加 deleted_at IS NULL, 已投递 (软删) 消息天然不可见
// 返回纯类型 TimeScaleMessage (orm 行映射见 toTimeScaleMessages)
func (s *GormMessageStore) FetchOffline(ctx context.Context, toID string, limit int) ([]message.TimeScaleMessage, error) {
	db, err := s.dbConn()
	if err != nil {
		return nil, err
	}
	var rows []ormchat.Message
	if err := db.WithContext(ctx).
		Where("to_id = ? AND group_type = ?", toID, constant.GroupPrivate).
		Order("id ASC").
		Limit(limit).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return toTimeScaleMessages(rows), nil
}

// FetchGroupHistory 拉取群聊历史 (to_id = 群 ID): 最新 limit 条, 反转回时序正序
// 返回纯类型 TimeScaleMessage (orm 行映射见 toTimeScaleMessages)
func (s *GormMessageStore) FetchGroupHistory(ctx context.Context, groupID string, limit int) ([]message.TimeScaleMessage, error) {
	db, err := s.dbConn()
	if err != nil {
		return nil, err
	}
	var rows []ormchat.Message
	if err := db.WithContext(ctx).
		Where("to_id = ? AND group_type = ?", groupID, constant.GroupGroup).
		Order("id DESC").
		Limit(limit).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	return toTimeScaleMessages(rows), nil
}

// DeleteByIDs 标记已投递消息为软删除 (保留历史可审计)
// 物理清理由 sidecar 定时任务批量执行 (硬删不在此路径, 避免阻塞投递)
func (s *GormMessageStore) DeleteByIDs(ctx context.Context, ids []uint) error {
	db, err := s.dbConn()
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	return db.WithContext(ctx).Delete(&ormchat.Message{}, ids).Error
}

// toTimeScaleMessages 将 orm 行映射为纯类型 TimeScaleMessage (GormMessageStore 读取路径)
// Payload 由 string 转 []byte (与 message.Unmarshal 输入一致)
func toTimeScaleMessages(rows []ormchat.Message) []message.TimeScaleMessage {
	out := make([]message.TimeScaleMessage, 0, len(rows))
	for _, r := range rows {
		out = append(out, message.TimeScaleMessage{
			ID:        r.ID,
			GroupType: r.GroupType,
			FromID:    r.FromID,
			ToID:      r.ToID,
			Payload:   []byte(r.Payload),
			CreatedAt: r.CreatedAt,
		})
	}
	return out
}
