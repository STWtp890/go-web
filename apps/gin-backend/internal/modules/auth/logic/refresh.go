package logic

import (
	"context"
	"errors"
	"fmt"

	"gin-backend/internal/common/service/jwt"
	"gin-backend/internal/config"
	authmodel "gin-backend/internal/model/orm/auth"
)

// RefreshTokenLogic 刷新 JWT token（签发新 token 对 → Redis 原子轮换）。
//
// refresh token 的验签由 HTTP 适配层完成并以 identity 传入, 本层不再解析 token,
// 也不再接触 JWT claims。
func RefreshTokenLogic(ctx context.Context, oldToken string, identity jwt.RefreshIdentity) (jwt.TokenPair, error) {
	if oldToken == "" {
		return jwt.TokenPair{}, errors.New("token 不能为空")
	}
	if identity.UserID == 0 || identity.SessionID == "" {
		return jwt.TokenPair{}, errors.New("无法解析用户信息")
	}

	jwtCfg := config.CustomConfig().JWT
	user := &authmodel.User{ID: identity.UserID, Email: identity.Email, Nickname: identity.Nickname}
	privateKey := jwtCfg.GetPrivateKey()

	accessToken, err := signToken(user, identity.SessionID, hourExpire(jwtCfg.AccessExpireHours), privateKey, jwt.TokenUseAccess)
	if err != nil {
		return jwt.TokenPair{}, err
	}
	newRefreshToken, err := signToken(user, identity.SessionID, hourExpire(jwtCfg.RefreshExpireHours), privateKey, jwt.TokenUseRefresh)
	if err != nil {
		return jwt.TokenPair{}, err
	}

	// 原子轮换：sid 必须仍是当前会话，且并发请求中仅一个能消费同一旧 token。
	rotated, err := jwt.RotateUserRefreshToken(
		ctx, identity.Subject, identity.SessionID, oldToken, newRefreshToken, hourExpire(jwtCfg.RefreshExpireHours),
	)
	if err != nil {
		return jwt.TokenPair{}, fmt.Errorf("刷新服务暂不可用: %w", err)
	}
	if !rotated {
		return jwt.TokenPair{}, errors.New("refresh token 已被使用或吊销")
	}

	return jwt.TokenPair{AccessToken: accessToken, RefreshToken: newRefreshToken}, nil
}
