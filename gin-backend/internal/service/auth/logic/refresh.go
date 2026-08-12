package logic

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"gin-backend/internal/common/service/jwtmethod"
	"gin-backend/internal/config"
	authmodel "gin-backend/internal/orm/auth"

	"github.com/golang-jwt/jwt/v5"
)

// RefreshTokenLogic 刷新 JWT token（验签旧 refreshToken → 白名单校验 → 签发新 token 对）
func RefreshTokenLogic(ctx context.Context, oldToken string) (*map[string]string, error) {
	if oldToken == "" {
		return nil, errors.New("token 不能为空")
	}

	jwtCfg := config.CustomConfig().JWT

	// 1. 验签并解析旧 refreshToken
	claims, err := jwtmethod.ParseTokenClaims(oldToken, jwtCfg.GetPublicKey())
	if err != nil {
		return nil, errors.New("无效的 refresh token")
	}

	// 2. 提取用户信息
	user, err := extractUserFromClaims(claims)
	if err != nil {
		return nil, errors.New("无法解析用户信息")
	}

	// 3. 白名单校验：旧 refreshToken 必须是当前有效的
	if !validateRefreshForRotate(ctx, fmt.Sprintf("%d", user.ID), oldToken) {
		return nil, errors.New("refresh token 已被使用或吊销")
	}

	// 4. 签发新 accessToken
	privateKey := jwtCfg.GetPrivateKey()
	accessToken, err := signToken(user,
		hourExpire(jwtCfg.AccessExpireHours),
		privateKey,
	)
	if err != nil {
		return nil, err
	}

	// 5. 轮换 refreshToken
	newRefreshToken, err := signToken(user,
		hourExpire(jwtCfg.RefreshExpireHours),
		privateKey,
	)
	if err != nil {
		return nil, err
	}

	// 6. 覆盖白名单：旧 RT 失效，新 RT 写入
	if err := storeRotatedRefresh(ctx, fmt.Sprintf("%d", user.ID), newRefreshToken); err != nil {
		return nil, err
	}

	return &map[string]string{
		"accessToken":  accessToken,
		"refreshToken": newRefreshToken,
	}, nil
}

func extractUserFromClaims(claims *jwt.MapClaims) (*authmodel.User, error) {
	sub, err := claims.GetSubject()
	if err != nil {
		return nil, err
	}
	email, ok := (*claims)["email"].(string)
	if !ok || email == "" {
		return nil, errors.New("无法解析用户邮箱")
	}
	nickname, ok := (*claims)["nickname"].(string)
	if !ok || nickname == "" {
		return nil, errors.New("无法解析用户昵称")
	}
	userID, err := strconv.ParseUint(sub, 10, 64)
	if err != nil {
		return nil, errors.New("无法解析用户ID")
	}
	return &authmodel.User{ID: uint(userID), Email: email, Nickname: nickname}, nil
}

// validateRefreshForRotate 刷新前校验：旧 RT 必须在白名单中
func validateRefreshForRotate(ctx context.Context, email, oldToken string) bool {
	return IsRefreshValid(ctx, email, oldToken)
}

// storeRotatedRefresh 刷新成功后：覆盖写入新 RT，旧 RT 立即失效
func storeRotatedRefresh(ctx context.Context, email, newToken string) error {
	return SetRefreshWhitelist(ctx, email, newToken)
}
