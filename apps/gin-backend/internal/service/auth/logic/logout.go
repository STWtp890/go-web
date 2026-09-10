package logic

import (
	"context"
	"errors"

	"gin-backend/internal/common/service/jwt"
	"gin-backend/internal/common/service/sessionevent"

	jwtlib "github.com/golang-jwt/jwt/v5"
)

// LogoutLogic 登出：条件删除当前 sid 会话。会话删除后，该 sid 的 Access 和
// Refresh Token 都会在下次使用时失效。
func LogoutLogic(ctx context.Context, claims jwtlib.MapClaims) error {
	// 从 claims 提取 subject (user.ID)
	sub, err := claims.GetSubject()
	if err != nil || sub == "" {
		return errors.New("无法解析用户身份")
	}
	sessionID, ok := jwt.SessionIDFromClaims(claims)
	if !ok {
		return errors.New("无法解析会话信息")
	}

	revoked, err := jwt.RevokeUserSessionIfCurrent(ctx, sub, sessionID)
	if err != nil {
		return err
	}
	if revoked {
		publishSessionRevoked(ctx, sessionevent.NewUserSessionRevokedEvent(sub, sessionID, sessionevent.RevokeReasonLogout))
	}
	return nil
}
