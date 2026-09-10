// 接入后补发：私聊与群聊统一从 pending delivery 真相源重放。
package bridge

import (
	"context"
	"log/slog"

	"gin-backend/internal/service/chat/store"
	"gin-backend/internal/service/chat/types/client"
)

// pendingFetchLimit 单次 pending delivery 重放上限。
const pendingFetchLimit = 100

// replayPending 分页重放当前接收者全部 pending delivery。
// ACK 前记录始终保留；连接断开或背压时，下次连接从数据库重新恢复。
func (b *MessageBridge) replayPending(ctx context.Context, subject string, uc *client.UserChannel) {
	succeeded := false
	defer func() {
		if !succeeded || !uc.FinishReplay() {
			b.Detach(subject, uc)
		}
	}()
	if b.messages == nil {
		return
	}
	ds, ok := b.messages.(store.DeliveryStore)
	if !ok {
		slog.Warn("可靠投递存储未初始化")
		return
	}
	cursor := store.DeliveryCursor{}
	for {
		rows, err := ds.FetchPending(ctx, subject, pendingFetchLimit, cursor)
		if err != nil {
			slog.Warn("拉取 pending delivery 失败", slog.String("subject", subject), slog.String("error", err.Error()))
			return
		}
		for _, row := range rows {
			if !uc.PushReplay(row.Message) {
				return
			}
			cursor = store.DeliveryCursor{CreatedAt: row.CreatedAt, DeliveryID: row.DeliveryID}
		}
		if len(rows) < pendingFetchLimit {
			succeeded = true
			return
		}
	}
}
