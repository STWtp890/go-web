package logic

import (
	"context"
	"errors"
	"fmt"

	"gin-backend/internal/common/service/jwt"
	"gin-backend/internal/config"
	authmodel "gin-backend/internal/model/orm/auth"

	jwtlib "github.com/golang-jwt/jwt/v5"
)

// RefreshTokenLogic 刷新 JWT token（验签旧 refreshToken → 白名单校验 → 签发新 token 对）
func RefreshTokenLogic(ctx context.Context, oldToken string) (*map[string]string, error) {
	if oldToken == "" {
		return nil, errors.New("token 不能为空")
	}

	jwtCfg := config.CustomConfig().JWT

	// 1. 验签并解析旧 refreshToken
	claims, err := jwt.ParseTokenClaims(oldToken, jwtCfg.GetPublicKey(), jwt.TokenUseRefresh)
	if err != nil {
		return nil, errors.New("无效的 refresh token")
	}

	// 2. 提取用户信息
	user, err := extractUserFromClaims(claims)
	if err != nil {
		return nil, errors.New("无法解析用户信息")
	}
	sessionID, ok := jwt.SessionIDFromClaims(*claims)
	if !ok {
		return nil, errors.New("refresh token 缺少会话信息")
	}

	// 3. 签发新 token 对，随后以 Redis 原子轮换旧 refresh token。
	privateKey := jwtCfg.GetPrivateKey()
	accessToken, err := signToken(user,
		sessionID,
		hourExpire(jwtCfg.AccessExpireHours),
		privateKey,
		jwt.TokenUseAccess,
	)
	if err != nil {
		return nil, err
	}

	// 5. 轮换 refreshToken
	newRefreshToken, err := signToken(user,
		sessionID,
		hourExpire(jwtCfg.RefreshExpireHours),
		privateKey,
		jwt.TokenUseRefresh,
	)
	if err != nil {
		return nil, err
	}

	// 4. 原子轮换：sid 必须仍是当前会话，且并发请求中仅一个能消费同一旧 token。
	ok, err = jwt.RotateUserRefreshToken(ctx, fmt.Sprintf("%d", user.ID), sessionID, oldToken, newRefreshToken, hourExpire(jwtCfg.RefreshExpireHours))
	if err != nil {
		return nil, fmt.Errorf("刷新服务暂不可用: %w", err)
	}
	if !ok {
		return nil, errors.New("refresh token 已被使用或吊销")
	}

	return &map[string]string{
		"accessToken":  accessToken,
		"refreshToken": newRefreshToken,
	}, nil
}

func extractUserFromClaims(claims *jwtlib.MapClaims) (*authmodel.User, error) {
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
	userID, ok := jwt.ParseSubject(sub)
	if !ok {
		return nil, errors.New("无法解析用户ID")
	}
	return &authmodel.User{ID: userID, Email: email, Nickname: nickname}, nil
}
