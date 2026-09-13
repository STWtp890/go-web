// 管理员刷新 token 对: 验签旧 refresh → 白名单校验 → 签发新对并覆盖白名单 (轮换)
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
// :Return
// - `*map[string]string` {accessToken, refreshToken}
// - `error` 刷新失败
func RefreshLogic(ctx context.Context, oldToken string) (*map[string]string, error) {
	if oldToken == "" {
		return nil, errors.New("token 不能为空")
	}
	jwtCfg := config.CustomConfig().JWT

	// 1. 验签并解析旧 refresh token
	claims, err := jwt.ParseTokenClaims(oldToken, jwtCfg.GetPublicKey(), jwt.TokenUseRefresh)
	if err != nil {
		return nil, errors.New("无效的 refresh token")
	}
	// 2. 校验角色: 仅管理员 token 可刷新
	if role, _ := (*claims)["role"].(string); role != "manager" {
		return nil, errors.New("非管理员 token")
	}
	// 3. 提取管理员身份
	sub, err := claims.GetSubject()
	if err != nil || sub == "" {
		return nil, errors.New("无法解析管理员身份")
	}
	sessionID, ok := jwt.SessionIDFromClaims(*claims)
	if !ok {
		return nil, errors.New("refresh token 缺少会话信息")
	}
	managerID, ok := jwt.ParseSubject(sub)
	if !ok {
		return nil, errors.New("无法解析管理员ID")
	}

	// 4. 重新加载管理员 (实体缓存读取, 校验存在与 active, 防停用后仍可刷新)
	mc, err := store.Manager.GetByID(ctx, managerID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("管理员不存在")
		}
		return nil, err
	}
	m := &managermodel.Manager{
		ID:       mc.ID,
		Username: mc.Username,
		Password: mc.Password,
		Email:    mc.Email,
		Status:   mc.Status,
	}
	if m.Status != managermodel.ManagerActive {
		return nil, errors.New("账号已停用")
	}

	// 5. 签发新双 token
	privateKey := jwtCfg.GetPrivateKey()
	accessToken, err := signManagerToken(m, sessionID, hourExpire(jwtCfg.AccessExpireHours), privateKey, jwt.TokenUseAccess)
	if err != nil {
		return nil, err
	}
	refreshExpire := hourExpire(jwtCfg.RefreshExpireHours)
	newRefreshToken, err := signManagerToken(m, sessionID, refreshExpire, privateKey, jwt.TokenUseRefresh)
	if err != nil {
		return nil, err
	}

	// 6. 原子轮换，避免同一 refresh token 被并发消费。
	rotated, err := jwt.RotateManagerRefreshToken(ctx, sub, sessionID, oldToken, newRefreshToken, refreshExpire)
	if err != nil {
		return nil, fmt.Errorf("刷新服务暂不可用: %w", err)
	}
	if !rotated {
		return nil, errors.New("refresh token 已被使用或吊销")
	}

	return &map[string]string{
		"accessToken":  accessToken,
		"refreshToken": newRefreshToken,
	}, nil
}
