package chat

import (
	"context"
	"log/slog"

	"gin-backend/internal/common/service/sessionevent"
)

// HandleSessionRevoked 关闭本应用实例中与撤销事件精确匹配的聊天连接。
func HandleSessionRevoked(_ context.Context, event sessionevent.SessionRevokedEvent) error {
	if event.Type != sessionevent.EventSessionRevoked || event.PrincipalType != sessionevent.PrincipalUser {
		return nil
	}

	h := Hub()
	if h == nil {
		return nil
	}

	closed := h.RevokeSession(event.PrincipalID, event.SessionID)
	slog.Info("chat_session_revoked",
		slog.String("event_id", event.EventID),
		slog.String("user_id", event.PrincipalID),
		slog.String("sid", event.SessionID),
		slog.String("reason", string(event.Reason)),
		slog.Int("closed_connections", closed),
	)
	return nil
}
