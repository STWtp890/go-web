// 管理员刷新 token 对: 凭据校验 → 签发新对 → Redis 原子轮换
package logic

import (
	"context"
	"errors"
	"fmt"

	"gin-backend/internal/common/service/jwt"
	"gin-backend/internal/config"
	managermodel "gin-backend/internal/model/orm/manager"
	"gin-backend/internal/model/store"

	"gorm.io/gorm"
)

// RefreshLogic 刷新管理员 token 对 (sid 当前性校验 + Refresh Token 原子轮换)。
//
// refresh token 的验签由 HTTP 适配层完成并以 identity 传入, 本层不再解析 token。
func RefreshLogic(ctx context.Context, oldToken string, identity jwt.RefreshIdentity) (jwt.TokenPair, error) {
	if oldToken == "" {
		return jwt.TokenPair{}, errors.New("token 不能为空")
	}
	if identity.Role != "manager" {
		return jwt.TokenPair{}, errors.New("非管理员 token")
	}
	if identity.SessionID == "" {
		return jwt.TokenPair{}, errors.New("refresh token 缺少会话信息")
	}
	if identity.UserID == 0 {
		return jwt.TokenPair{}, errors.New("无法解析管理员ID")
	}

	// 重新加载管理员 (实体缓存读取, 校验存在与 active, 防停用后仍可刷新)
	mc, err := store.Manager.GetByID(ctx, identity.UserID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return jwt.TokenPair{}, errors.New("管理员不存在")
		}
		return jwt.TokenPair{}, err
	}
	m := &managermodel.Manager{
		ID:       mc.ID,
		Username: mc.Username,
		Password: mc.Password,
		Email:    mc.Email,
		Status:   mc.Status,
	}
	if m.Status != managermodel.ManagerActive {
		return jwt.TokenPair{}, errors.New("账号已停用")
	}

	jwtCfg := config.CustomConfig().JWT
	privateKey := jwtCfg.GetPrivateKey()
	accessToken, err := signManagerToken(m, identity.SessionID, hourExpire(jwtCfg.AccessExpireHours), privateKey, jwt.TokenUseAccess)
	if err != nil {
		return jwt.TokenPair{}, err
	}
	refreshExpire := hourExpire(jwtCfg.RefreshExpireHours)
	newRefreshToken, err := signManagerToken(m, identity.SessionID, refreshExpire, privateKey, jwt.TokenUseRefresh)
	if err != nil {
		return jwt.TokenPair{}, err
	}

	// 原子轮换，避免同一 refresh token 被并发消费。
	rotated, err := jwt.RotateManagerRefreshToken(ctx, identity.Subject, identity.SessionID, oldToken, newRefreshToken, refreshExpire)
	if err != nil {
		return jwt.TokenPair{}, fmt.Errorf("刷新服务暂不可用: %w", err)
	}
	if !rotated {
		return jwt.TokenPair{}, errors.New("refresh token 已被使用或吊销")
	}

	return jwt.TokenPair{AccessToken: accessToken, RefreshToken: newRefreshToken}, nil
}
