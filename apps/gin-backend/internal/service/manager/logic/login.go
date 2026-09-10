// 管理员登录 (审批通过后的 active 账号可登录, 签发双 token 支持吊销/刷新)
package logic

import (
	"context"
	"crypto/rsa"
	"errors"
	"fmt"
	"time"

	"gin-backend/internal/common/service/jwt"
	"gin-backend/internal/config"
	managermodel "gin-backend/internal/model/orm/manager"
	"gin-backend/internal/model/store"
	"gin-backend/internal/service/manager/types/requests"

	jwtlib "github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// LoginLogic 管理员登录: 校验账号 active + bcrypt 密码, 签发绑定 sid 的双 token。
// Redis 仅保存当前 sid，因此新登录会使旧端 Access/Refresh Token 同时失效。
// :Return
// - `string` access token (claims: sub=managers.id, role=manager, username)
// - `string` refresh token
// - `error` 登录失败
func LoginLogic(ctx context.Context, req *requests.LoginRequest) (string, string, error) {
	// 1. 按用户名读取管理员 (实体缓存: Redis 主 → 内存回退 → DB 回源)
	mc, err := store.Manager.GetByUsername(ctx, req.Username)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// 统一错误, 不泄露用户是否存在
			return "", "", errors.New("用户名或密码错误")
		}
		return "", "", err
	}
	// 缓存 DTO → ORM 模型 (LoginLogic 其余流程不变)
	m := &managermodel.Manager{
		ID:       mc.ID,
		Username: mc.Username,
		Password: mc.Password,
		Email:    mc.Email,
		Status:   mc.Status,
	}
	if m.Status != managermodel.ManagerActive {
		return "", "", errors.New("账号已停用")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(m.Password), []byte(req.Password)); err != nil {
		return "", "", errors.New("用户名或密码错误")
	}

	// 创建管理员会话并签发双 token (role=manager, 供 ManagerAuthRequired 识别)。
	sessionID, err := jwt.NewSessionID()
	if err != nil {
		return "", "", err
	}
	privateKey := config.CustomConfig().JWT.GetPrivateKey()
	accessToken, err := signManagerToken(m, sessionID, hourExpire(config.CustomConfig().JWT.AccessExpireHours), privateKey, jwt.TokenUseAccess)
	if err != nil {
		return "", "", err
	}
	refreshExpire := hourExpire(config.CustomConfig().JWT.RefreshExpireHours)
	refreshToken, err := signManagerToken(m, sessionID, refreshExpire, privateKey, jwt.TokenUseRefresh)
	if err != nil {
		return "", "", err
	}

	// 原子覆盖管理员当前会话，旧 sid 的 Token 在下一次使用时立即失效。
	if _, err := jwt.CreateOrReplaceManagerSession(ctx, fmt.Sprintf("%d", m.ID), sessionID, refreshToken, refreshExpire); err != nil {
		return "", "", err
	}

	return accessToken, refreshToken, nil
}

// signManagerToken 签发管理员 JWT (RS256, role=manager)
func signManagerToken(m *managermodel.Manager, sessionID string, expire time.Duration, privateKey *rsa.PrivateKey, tokenUse string) (string, error) {
	now := jwtlib.NewNumericDate(time.Now())
	token := jwtlib.NewWithClaims(jwtlib.SigningMethodRS256, jwtlib.MapClaims{
		"iss":       config.CustomConfig().JWT.Issuer,
		"token_use": tokenUse,
		"sid":       sessionID,
		"jti":       uuid.New().String(),
		"iat":       now,
		"nbf":       now,
		"exp":       jwtlib.NewNumericDate(now.Add(expire)),
		"sub":       fmt.Sprintf("%d", m.ID),
		"role":      "manager",
		"username":  m.Username,
	})
	signed, err := token.SignedString(privateKey)
	if err != nil {
		return "", errors.New("生成 token 失败")
	}
	return signed, nil
}

func hourExpire(expireHours uint) time.Duration {
	return time.Duration(expireHours) * time.Hour
}
