package logic

import (
	"context"
	"crypto/rsa"
	"errors"
	"fmt"
	"time"

	"gin-backend/internal/common/connection"
	postgresqlconn "gin-backend/internal/common/connection/postgresql"
	"gin-backend/internal/config"
	authmodel "gin-backend/internal/orm/auth"
	"gin-backend/internal/service/auth/types/requests"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
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
	// 检查用户是否存在
	user, err := checkLoginUserExists(req.Email)
	if err != nil {
		return "", "", err
	}

	// 检查密码是否正确
	if err := compareLoginPassword(user.Password, req.Password); err != nil {
		return "", "", err
	}

	// 生成 JWT token
	privateKey := config.CustomConfig().JWT.GetPrivateKey() // 获取 RSA 私钥
	// Access JWT token
	accessExpire := hourExpire(config.CustomConfig().JWT.AccessExpireHours)
	accessToken, err := signToken(user, accessExpire, privateKey)
	if err != nil {
		return "", "", err
	}
	// Refresh JWT token
	refreshExpire := hourExpire(config.CustomConfig().JWT.RefreshExpireHours)
	refreshToken, err := signToken(user, refreshExpire, privateKey)
	if err != nil {
		return "", "", err
	}

	// 登录成功 → 将 refreshToken 写入白名单
	if err := storeRefreshToken(ctx, fmt.Sprintf("%d", user.ID), refreshToken); err != nil {
		return "", "", err
	}

	return accessToken, refreshToken, nil
}

// checkLoginUserExists 检查用户是否存在
// :Param
// - `email` 用户邮箱
// :Return
// - `*authmodel.User` 用户信息
// - `error` 如果用户不存在，返回错误信息
func checkLoginUserExists(email string) (*authmodel.User, error) {
	conn, err := postgresqlconn.PostgreSQLManager.Get(connection.ServiceAuth)
	if err != nil {
		return nil, err
	}
	db, err := conn.GetConn()
	if err != nil {
		return nil, err
	}

	var user authmodel.User
	result := db.Where("email = ?", email).First(&user)
	if result.Error != nil {
		return nil, errors.New("用户不存在")
	}
	return &user, nil
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
func signToken(user *authmodel.User, expire time.Duration, privateKey *rsa.PrivateKey) (string, error) {
	now := jwt.NewNumericDate(time.Now())
	jti := UUID()
	token := jwt.NewWithClaims(
		jwt.SigningMethodRS256,
		jwt.MapClaims{
			"jti":      jti,
			"iat":      now,
			"nbf":      now,
			"exp":      jwt.NewNumericDate(now.Add(expire)),
			"sub":      fmt.Sprintf("%d", user.ID),
			"email":    user.Email,
			"nickname": user.Nickname,
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

// ==================== 登录专属 Redis 封装 ====================

// storeRefreshToken 登录成功后将 refreshToken 写入白名单
// Redis 写入失败不影响登录（降级策略：允许登录，但后续刷新会失败）
func storeRefreshToken(ctx context.Context, email, refreshToken string) error {
	return SetRefreshWhitelist(ctx, email, refreshToken)
}

func UUID() string {
	return uuid.New().String()
}
