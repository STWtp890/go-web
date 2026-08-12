package logic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"gin-backend/internal/common/connection"
	redisconn "gin-backend/internal/common/connection/redis"
	"gin-backend/internal/config"
)

const (
	// Redis Key 前缀
	prefixAccessBlacklist  = "jwt:access:bl:"  // 黑名单：被吊销的 access token
	prefixRefreshWhitelist = "jwt:refresh:wl:" // 白名单：用户当前有效的 refresh token
)

// AddAccessBlacklist 将 access token 加入黑名单（登出/封禁时调用）
// 用 jti (JWT ID) 的 SHA256 前 16 位作为 key，避免存储完整 token
// :Param
// - `ctx` 上下文
// - `accessToken` 用户的 access token
// :Return
// - `error` 如果加入黑名单失败，返回错误信息
func AddAccessBlacklist(ctx context.Context, accessToken string) error {
	conn, err := redisconn.RedisManager.Get(connection.ServiceAuth)
	if err != nil {
		return err
	}
	rdb, err := conn.GetConn()
	if err != nil {
		return err
	}

	// 1. 计算 jti SHA256 前 16 位 hex
	jti := hashToken(accessToken)

	// 2. 设置 Redis key，过期时间为 access token 的剩余有效期
	ttl := time.Duration(config.CustomConfig().JWT.AccessExpireHours) * time.Hour

	// 3. 存入 Redis
	key := prefixAccessBlacklist + jti
	return rdb.Set(ctx, key, "1", ttl).Err()
}

// IsAccessBlacklisted 检查 access token 是否在黑名单中
// :Param
// - `ctx` 上下文
// - `accessToken` 用户的 access token
// :Return
// - `bool` 如果在黑名单中返回 true，否则返回 false
func IsAccessBlacklisted(ctx context.Context, accessToken string) bool {
	conn, err := redisconn.RedisManager.Get(connection.ServiceAuth)
	if err != nil {
		return false
	}
	rdb, err := conn.GetConn()
	if err != nil {
		return false
	}

	// 1. 计算 jti SHA256 前 16 位 hex
	jti := hashToken(accessToken)
	// 2. 检查 Redis 是否存在该 key
	key := prefixAccessBlacklist + jti
	n, err := rdb.Exists(ctx, key).Result()
	if err != nil {
		return false
	}
	return n > 0
}

// SetRefreshWhitelist 存储用户当前有效的 refresh token（登录/刷新时调用）
// 每个用户只保留最新一个，旧值自动被覆盖
func SetRefreshWhitelist(ctx context.Context, email, refreshToken string) error {
	conn, err := redisconn.RedisManager.Get(connection.ServiceAuth)
	if err != nil {
		return err
	}
	rdb, err := conn.GetConn()
	if err != nil {
		return err
	}

	ttl := time.Duration(config.CustomConfig().JWT.RefreshExpireHours) * time.Hour

	key := prefixRefreshWhitelist + email
	val := hashToken(refreshToken)

	return rdb.Set(ctx, key, val, ttl).Err()
}

// IsRefreshValid 验证 refresh token 是否是用户当前有效的那个
func IsRefreshValid(ctx context.Context, email, refreshToken string) bool {
	conn, err := redisconn.RedisManager.Get(connection.ServiceAuth)
	if err != nil {
		return false
	}
	rdb, err := conn.GetConn()
	if err != nil {
		return false
	}

	key := prefixRefreshWhitelist + email
	val, err := rdb.Get(ctx, key).Result()
	if err != nil {
		return false
	}
	return val == hashToken(refreshToken)
}

// RemoveRefreshWhitelist 删除用户的 refresh token（强制下线）
func RemoveRefreshWhitelist(ctx context.Context, email string) error {
	conn, err := redisconn.RedisManager.Get(connection.ServiceAuth)
	if err != nil {
		return err
	}
	rdb, err := conn.GetConn()
	if err != nil {
		return err
	}

	key := prefixRefreshWhitelist + email
	return rdb.Del(ctx, key).Err()
}

// hashToken 对 token 做 SHA256 取前 16 位 hex，避免在 Redis 中存储完整 JWT
// :Param
// - `token` 用户的 JWT token
// :Return
// - `string` token 的 SHA256 前 16 位 hex 字符串
func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:8]) // 取前 8 字节 = 16 位 hex
}
