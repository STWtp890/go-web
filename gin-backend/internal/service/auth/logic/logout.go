package logic

import (
	"context"
	"errors"

	"github.com/golang-jwt/jwt/v5"
)

// LogoutLogic 登出：AT 加入黑名单 + RT 删除白名单
func LogoutLogic(ctx context.Context, accessToken string, claims jwt.MapClaims) error {
	// 1. Access Token → 黑名单
	if err := AddAccessBlacklist(ctx, accessToken); err != nil {
		return err
	}

	// 2. 从 claims 提取 subject (user.ID)
	sub, err := claims.GetSubject()
	if err != nil || sub == "" {
		return errors.New("无法解析用户身份")
	}

	// 3. Refresh Token → 删除白名单（强制下线）
	return RemoveRefreshWhitelist(ctx, sub)
}
