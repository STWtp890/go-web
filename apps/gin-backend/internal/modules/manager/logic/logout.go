// 管理员登出：条件删除当前 sid 会话 (Redis 吊销)
package logic

import (
	"context"
	"errors"

	"gin-backend/internal/common/service/jwt"

	jwtlib "github.com/golang-jwt/jwt/v5"
)

// LogoutLogic 管理员登出: 当前 sid 会话删除后，AT 与 RT 均立即失效。
func LogoutLogic(ctx context.Context, claims jwtlib.MapClaims) error {
	// 提取 manager id
	sub, err := claims.GetSubject()
	if err != nil || sub == "" {
		return errors.New("无法解析管理员身份")
	}
	sessionID, ok := jwt.SessionIDFromClaims(claims)
	if !ok {
		return errors.New("无法解析会话信息")
	}

	_, err = jwt.RevokeManagerSessionIfCurrent(ctx, sub, sessionID)
	return err
}
