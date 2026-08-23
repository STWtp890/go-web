package store

import (
	"context"
	"fmt"

	ormchat "gin-backend/internal/model/orm/chat"
	"gin-backend/internal/service/chat/types/message"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Delivery 是带接收者专属 ID 的可确认消息。
type Delivery struct {
	DeliveryID  string
	MessageID   uint
	RecipientID string
	CreatedAt   int64
	Message     message.Message
}

// DeliveryCursor 是按 (created_at, delivery_id) 排序的稳定重放游标。
type DeliveryCursor struct {
	CreatedAt  int64
	DeliveryID string
}

// DeliveryStore 提供“先持久化后投递”的至少一次语义。
type DeliveryStore interface {
	SaveWithDeliveries(context.Context, message.Message, []string) ([]Delivery, error)
	FetchPending(context.Context, string, int, DeliveryCursor) ([]Delivery, error)
	Ack(context.Context, string, []string) error
}

// SaveWithDeliveries 将消息及其接收者列表持久化到数据库，并返回每个接收者的 Delivery。
// 该方法在事务中执行，确保消息和接收者列表的原子性。
func (s *TimescaleMessageStore) SaveWithDeliveries(ctx context.Context, m message.Message, recipients []string) ([]Delivery, error) {
	db, err := s.dbConn()
	if err != nil {
		return nil, err
	}
	if len(recipients) == 0 {
		return nil, fmt.Errorf("没有消息接收者")
	}
	var out []Delivery
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row := ormchat.Message{GroupType: m.GroupType(), FromID: m.From(), ToID: m.To(), Payload: string(m.Marshal())}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		rows := make([]ormchat.MessageDelivery, 0, len(recipients))
		for _, recipient := range recipients {
			id := uuid.NewString()
			rows = append(rows, ormchat.MessageDelivery{DeliveryID: id, MessageID: row.ID, MessageCreatedAt: row.CreatedAt, RecipientID: recipient})
		}
		if err := tx.Create(&rows).Error; err != nil {
			return err
		}
		out = make([]Delivery, 0, len(rows))
		for _, deliveryRow := range rows {
			out = append(out, Delivery{
				DeliveryID:  deliveryRow.DeliveryID,
				MessageID:   row.ID,
				RecipientID: deliveryRow.RecipientID,
				CreatedAt:   deliveryRow.CreatedAt,
				Message:     withDeliveryMetadata(m, row.ID, deliveryRow.DeliveryID),
			})
		}
		return nil
	})
	return out, err
}

// FetchPending 获取指定接收者待处理的消息。
func (s *TimescaleMessageStore) FetchPending(ctx context.Context, recipient string, limit int, cursor DeliveryCursor) ([]Delivery, error) {
	db, err := s.dbConn()
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	type row struct {
		DeliveryID string
		MessageID  uint
		CreatedAt  int64
		Payload    string
	}
	var rows []row
	q := db.WithContext(ctx).Table("chat_message_deliveries d").
		Select("d.delivery_id, d.message_id, d.created_at, m.payload").
		Joins("JOIN chat_messages m ON m.id = d.message_id AND m.created_at = d.message_created_at").
		Where("d.recipient_id = ?", recipient).
		Order("d.created_at ASC, d.delivery_id ASC").Limit(limit)
	if cursor.DeliveryID != "" {
		q = q.Where("(d.created_at, d.delivery_id) > (?, ?)", cursor.CreatedAt, cursor.DeliveryID)
	}
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Delivery, 0, len(rows))
	for _, r := range rows {
		m, err := message.Unmarshal([]byte(r.Payload))
		if err != nil {
			return nil, err
		}
		out = append(out, Delivery{
			DeliveryID:  r.DeliveryID,
			MessageID:   r.MessageID,
			RecipientID: recipient,
			CreatedAt:   r.CreatedAt,
			Message:     withDeliveryMetadata(m, r.MessageID, r.DeliveryID),
		})
	}
	return out, nil
}

// Ack 删除当前接收者已由应用成功处理的 pending delivery。
// 不存在、重复或属于其他接收者的 ID 都是幂等成功。
func (s *TimescaleMessageStore) Ack(ctx context.Context, recipient string, deliveryIDs []string) error {
	db, err := s.dbConn()
	if err != nil {
		return err
	}
	if len(deliveryIDs) == 0 {
		return nil
	}
	return db.WithContext(ctx).
		Where("recipient_id = ? AND delivery_id IN ?", recipient, deliveryIDs).
		Delete(&ormchat.MessageDelivery{}).Error
}

func withDeliveryMetadata(m message.Message, messageID uint, deliveryID string) message.Message {
	o := m.ToOrigin()
	o.MetaData.DeliveryID = deliveryID
	o.MetaData.MessageID = messageID
	built, _ := message.FromOrigin(o)
	return built
}
