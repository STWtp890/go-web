// 接入后补发 (懒加载接入): 私聊离线 flushOffline + 群懒加载/窗口补发 joinUserGroups/loadGroup
package bridge

import (
	"context"
	"log/slog"

	"gin-backend/internal/service/chat/store"
	"gin-backend/internal/service/chat/types/client"
)

// offlineFetchLimit 单次离线补发 / 群历史拉取上限
const offlineFetchLimit = 100

// flushOffline 接入后异步补发私聊离线消息 (仅删除已成功投递, 连接断开时剩余保留)
func (b *MessageBridge) flushOffline(ctx context.Context, subject string, uc *client.UserChannel, lastEventID string) {
	if b.messages == nil {
		return
	}
	ds, ok := b.messages.(store.DeliveryStore)
	if !ok {
		slog.Warn("可靠投递存储未初始化")
		return
	}
	rows, err := ds.FetchPending(ctx, subject, offlineFetchLimit, lastEventID)
	if err != nil {
		slog.Warn("拉取离线消息失败", slog.String("subject", subject), slog.String("error", err.Error()))
		return
	}
	for _, row := range rows {
		if !uc.Push(row.Message) { // 连接已断开，保持未确认状态待下次补发
			break
		}
	}
}

// joinUserGroups 接入后异步: 对"在群"用户懒加载其聊天室 (成员+窗口) 并补发窗口消息
func (b *MessageBridge) joinUserGroups(ctx context.Context, subject string, uc *client.UserChannel) {
	gids, err := b.groupMgr.MyGroups(ctx, subject)
	if err != nil {
		slog.Warn("拉取用户群列表失败", slog.String("subject", subject), slog.String("error", err.Error()))
		return
	}
	for _, gid := range gids {
		s := b.groupMgr.Groups().Router(gid)
		if !s.Loaded() {
			b.groupMgr.EnsureLoaded(ctx, gid, s)
		}
		for _, m := range s.Join(subject) {
			if !uc.Push(m) {
				break
			}
		}
	}
}
