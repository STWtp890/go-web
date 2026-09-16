// 管理员登出：条件删除当前 sid 会话 (Redis 吊销)
package logic

import (
	"context"
	"errors"

	"gin-backend/internal/common/service/jwt"
)

// LogoutLogic 管理员登出: 当前 sid 会话删除后，AT 与 RT 均立即失效。
//
// 身份由 HTTP 适配层从中间件注入的 identity.Principal 提供, 本层不接触 JWT claims。
func LogoutLogic(ctx context.Context, subject, sessionID string) error {
	if subject == "" || sessionID == "" {
		return errors.New("无法解析管理员身份")
	}
	_, err := jwt.RevokeManagerSessionIfCurrent(ctx, subject, sessionID)
	return err
}
