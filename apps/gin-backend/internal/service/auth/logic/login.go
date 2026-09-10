package logic

import (
	"context"
	"crypto/rsa"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"gin-backend/internal/common/service/jwt"
	"gin-backend/internal/common/service/sessionevent"
	"gin-backend/internal/config"
	authmodel "gin-backend/internal/model/orm/auth"
	"gin-backend/internal/model/store"
	"gin-backend/internal/service/auth/types/requests"

	jwtlib "github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// LoginLogic 登录校验，返回 JWT token
// :Param
// - `email` 用户邮箱
// - `password` 用户密码
// :Return
// - `string` JWT access token
// - `string` JWT refresh token
// - `error` 如果登录失败，返回错误信息
func LoginLogic(ctx context.Context, req *requests.LoginRequest) (string, string, error) {
	// 检查用户是否存在 (实体缓存: Redis 主 → 内存回退 → DB 回源)
	user, err := checkLoginUserExists(ctx, req.Email)
	if err != nil {
		return "", "", err
	}

	// 检查账号是否被封禁 (缓存含 Banned 字段)
	if user.Banned {
		return "", "", errors.New("账号已被封禁")
	}

	// 检查密码是否正确
	if err := compareLoginPassword(user.Password, req.Password); err != nil {
		return "", "", err
	}

	// 创建新会话。一个用户在 Redis 中只保留一个 sid；后续登录会覆盖旧 sid。
	sessionID, err := jwt.NewSessionID()
	if err != nil {
		return "", "", err
	}

	// 生成 JWT token
	privateKey := config.CustomConfig().JWT.GetPrivateKey() // 获取 RSA 私钥
	// Access JWT token
	accessExpire := hourExpire(config.CustomConfig().JWT.AccessExpireHours)
	accessToken, err := signToken(user, sessionID, accessExpire, privateKey, jwt.TokenUseAccess)
	if err != nil {
		return "", "", err
	}
	// Refresh JWT token
	refreshExpire := hourExpire(config.CustomConfig().JWT.RefreshExpireHours)
	refreshToken, err := signToken(user, sessionID, refreshExpire, privateKey, jwt.TokenUseRefresh)
	if err != nil {
		return "", "", err
	}

	// 登录成功 → 原子覆盖该用户当前会话，旧端的 Access/Refresh Token
	// 会因 sid 不再匹配而在下次使用时立即失效。
	userID := fmt.Sprintf("%d", user.ID)
	oldSessionID, err := jwt.CreateOrReplaceUserSession(ctx, userID, sessionID, refreshToken, refreshExpire)
	if err != nil {
		return "", "", err
	}
	if oldSessionID != "" && oldSessionID != sessionID {
		publishSessionRevoked(ctx, sessionevent.NewUserSessionRevokedEvent(userID, oldSessionID, sessionevent.RevokeReasonLoginReplaced))
	}

	return accessToken, refreshToken, nil
}

// publishSessionRevoked 仅影响业务资源清理，不影响已经完成的会话状态变更。
// sid 校验始终是 Token 有效性的事实来源；发布失败在此记录并由后续基础设施观测。
func publishSessionRevoked(ctx context.Context, event sessionevent.SessionRevokedEvent) {
	if err := sessionevent.Publish(ctx, event); err != nil {
		slog.Error("session_revocation_event_publish_failed",
			slog.String("event_id", event.EventID),
			slog.String("user_id", event.PrincipalID),
			slog.String("sid", event.SessionID),
			slog.String("error", err.Error()),
		)
	}
}

// checkLoginUserExists 检查用户是否存在
// 实体缓存优先: GetByEmail 经 Redis(主) → MemCache(回退) → DB 回源 读取,
// 未命中回源 users 表后回填两级缓存; 未找到透传 gorm.ErrRecordNotFound
// :Param
// - `ctx` 上下文
// - `email` 用户邮箱
// :Return
// - `*authmodel.User` 用户信息 (自缓存 DTO 转换, 含 Password/Banned)
// - `error` 用户不存在 / 回源失败
func checkLoginUserExists(ctx context.Context, email string) (*authmodel.User, error) {
	cu, err := store.User.GetByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("用户不存在")
		}
		return nil, err
	}
	// 缓存 DTO → ORM 模型 (LoginLogic 其余流程不变)
	return &authmodel.User{
		ID:       cu.ID,
		Email:    cu.Email,
		Password: cu.Password,
		Nickname: cu.Nickname,
		Avatar:   cu.Avatar,
		Banned:   cu.Banned,
	}, nil
}

// compareLoginPassword 比较用户提交的密码和数据库中存储的哈希密码
// :Param
// - `actualPassword` 数据库中存储的哈希密码
// - `submittedPassword` 用户提交的明文密码
// :Return
// - `error` 如果密码不匹配，返回错误信息
func compareLoginPassword(actualPassword, submittedPassword string) error {
	err := bcrypt.CompareHashAndPassword([]byte(actualPassword), []byte(submittedPassword))
	if err != nil {
		return errors.New("密码错误")
	}
	return nil
}

// signToken 生成 JWT token（RS256 非对称签名）
// :Param
// - `user` 用户信息
// - `expire` token 过期时间
// - `privateKey` RSA 私钥
// :Return
// - `string` 生成的 JWT token
// - `error` 如果生成 token 失败，返回错误信息
func signToken(user *authmodel.User, sessionID string, expire time.Duration, privateKey *rsa.PrivateKey, tokenUse string) (string, error) {
	now := jwtlib.NewNumericDate(time.Now())
	jti := UUIDv4()
	token := jwtlib.NewWithClaims(
		jwtlib.SigningMethodRS256,
		jwtlib.MapClaims{
			"iss":       config.CustomConfig().JWT.Issuer,
			"token_use": tokenUse,
			"sid":       sessionID,
			"jti":       jti,
			"iat":       now,
			"nbf":       now,
			"exp":       jwtlib.NewNumericDate(now.Add(expire)),
			"sub":       fmt.Sprintf("%d", user.ID),
			"email":     user.Email,
			"nickname":  user.Nickname,
		},
	)

	signed, err := token.SignedString(privateKey)
	if err != nil {
		return "", errors.New("生成 token 失败")
	}

	return signed, nil
}

func hourExpire(expireHours uint) time.Duration {
	return time.Duration(expireHours) * time.Hour
}

// ==================== 工具 ====================

func UUIDv4() string {
	return uuid.New().String()
}
