package logic

import (
	"context"
	"errors"

	"gin-backend/internal/common/service/jwt"
	"gin-backend/internal/common/service/sessionevent"
)

// LogoutLogic 登出：条件删除当前 sid 会话。会话删除后，该 sid 的 Access 和
// Refresh Token 都会在下次使用时失效。
//
// 身份由 HTTP 适配层从中间件注入的 identity.Principal 提供, 本层不接触 JWT claims。
func LogoutLogic(ctx context.Context, subject, sessionID string) error {
	if subject == "" || sessionID == "" {
		return errors.New("无法解析用户身份")
	}

	revoked, err := jwt.RevokeUserSessionIfCurrent(ctx, subject, sessionID)
	if err != nil {
		return err
	}
	if revoked {
		publishSessionRevoked(ctx, sessionevent.NewUserSessionRevokedEvent(subject, sessionID, sessionevent.RevokeReasonLogout))
	}
	return nil
}
