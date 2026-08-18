package store

import (
	"context"
	"fmt"
	"time"

	ormchat "gin-backend/internal/model/orm/chat"
	"gin-backend/internal/service/chat/types/message"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Delivery 是带接收者专属 ID 的可确认消息。
type Delivery struct {
	DeliveryID  string
	RecipientID string
	Message     message.Message
}

// DeliveryStore 提供“先持久化后投递”的至少一次语义。
type DeliveryStore interface {
	SaveWithDeliveries(context.Context, message.Message, []string) ([]Delivery, error)
	FetchPending(context.Context, string, int, string) ([]Delivery, error)
	Acknowledge(context.Context, string, string) (bool, error)
}

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
		out = make([]Delivery, 0, len(recipients))
		for _, recipient := range recipients {
			id := uuid.NewString()
			rows = append(rows, ormchat.MessageDelivery{DeliveryID: id, MessageID: row.ID, MessageCreatedAt: row.CreatedAt, RecipientID: recipient})
			out = append(out, Delivery{DeliveryID: id, RecipientID: recipient, Message: withDeliveryID(m, id)})
		}
		return tx.Create(&rows).Error
	})
	return out, err
}

func (s *TimescaleMessageStore) FetchPending(ctx context.Context, recipient string, limit int, afterID string) ([]Delivery, error) {
	db, err := s.dbConn()
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	type row struct {
		DeliveryID string
		Payload    string
	}
	var rows []row
	q := db.WithContext(ctx).Table("chat_message_deliveries d").
		Select("d.delivery_id, m.payload").
		Joins("JOIN chat_messages m ON m.id = d.message_id AND m.created_at = d.message_created_at").
		Where("d.recipient_id = ? AND d.acknowledged_at IS NULL", recipient).
		Order("d.created_at ASC").Limit(limit)
	if afterID != "" {
		q = q.Where("d.delivery_id <> ?", afterID)
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
		out = append(out, Delivery{DeliveryID: r.DeliveryID, RecipientID: recipient, Message: withDeliveryID(m, r.DeliveryID)})
	}
	return out, nil
}

func (s *TimescaleMessageStore) Acknowledge(ctx context.Context, recipient, deliveryID string) (bool, error) {
	db, err := s.dbConn()
	if err != nil {
		return false, err
	}
	now := time.Now().Unix()
	result := db.WithContext(ctx).Model(&ormchat.MessageDelivery{}).
		Where("delivery_id = ? AND recipient_id = ? AND acknowledged_at IS NULL", deliveryID, recipient).
		Updates(map[string]any{"acknowledged_at": now})
	if result.Error != nil {
		return false, result.Error
	}
	if result.RowsAffected == 1 {
		return true, nil
	}
	var count int64
	err = db.WithContext(ctx).Model(&ormchat.MessageDelivery{}).Where("delivery_id = ? AND recipient_id = ?", deliveryID, recipient).Count(&count).Error
	return count == 1, err
}

func withDeliveryID(m message.Message, deliveryID string) message.Message {
	o := m.ToOrigin()
	o.MetaData.DeliveryID = deliveryID
	built, _ := message.FromOrigin(o)
	return built
}
