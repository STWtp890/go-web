// token 状态管理: accessToken 黑名单 + refreshToken 白名单
// 存储后端复用 common/base/cache 通用缓存存储: 优先 Redis (RedisCache),
// Redis 不可用时回退应用内缓存 (MemCache)
// 通用实现, auth(用户) 与 manager(管理员) 共用;
// 白名单 identifier 需调用方携带命名空间 (如 "user:1" / "manager:1") 防跨类型冲突
package jwt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"gin-backend/internal/config"

	goredis "github.com/redis/go-redis/v9"
)

// Redis Key 前缀
const (
	prefixAccessBlacklist  = "jwt:access:bl:"  // 黑名单：被吊销的 access token
	prefixRefreshWhitelist = "jwt:refresh:wl:" // 白名单：主体当前有效的 refresh token
)

// AddAccessBlacklist 将 access token 加入黑名单（登出/封禁时调用）
// 优先写 Redis; Redis 不可用时回退应用内缓存 (降级, 保证吊销基本生效)
// 用 token 的 SHA256 前 16 位 hex 作为 key，避免存储完整 JWT
func AddAccessBlacklist(ctx context.Context, accessToken string) error {
	key := prefixAccessBlacklist + hashToken(accessToken)
	ttl := time.Duration(config.CustomConfig().JWT.AccessExpireHours) * time.Hour
	cli, err := authRedis()
	if err != nil {
		return err
	}
	return cli.Set(ctx, key, "1", ttl).Err()
}

// IsAccessBlacklisted 检查 access token 是否在黑名单中
// 优先查 Redis; Redis 正常未命中返回 false, Redis 故障时回退应用内缓存
func IsAccessBlacklisted(ctx context.Context, accessToken string) (bool, error) {
	key := prefixAccessBlacklist + hashToken(accessToken)
	cli, err := authRedis()
	if err != nil {
		return false, err
	}
	_, err = cli.Get(ctx, key).Result()
	if err == nil {
		return true, nil
	}
	if err == goredis.Nil {
		return false, nil
	}
	return false, err
}

// SetRefreshWhitelist 存储主体当前有效的 refresh token（登录/刷新时调用）
// 每个主体只保留最新一个，旧值自动被覆盖; 优先 Redis, 失败回退内存
func SetRefreshWhitelist(ctx context.Context, identifier, refreshToken string) error {
	key := prefixRefreshWhitelist + identifier
	val := hashToken(refreshToken)
	ttl := time.Duration(config.CustomConfig().JWT.RefreshExpireHours) * time.Hour
	cli, err := authRedis()
	if err != nil {
		return err
	}
	return cli.Set(ctx, key, val, ttl).Err()
}

// IsRefreshValid 验证 refresh token 是否是主体当前有效的那个
// 优先查 Redis; Redis 正常未命中返回 false, Redis 故障时回退应用内缓存
func ConsumeRefreshWhitelist(ctx context.Context, identifier, oldToken, newToken string) (bool, error) {
	key := prefixRefreshWhitelist + identifier
	cli, err := authRedis()
	if err != nil {
		return false, err
	}
	ttl := time.Duration(config.CustomConfig().JWT.RefreshExpireHours) * time.Hour
	const rotateScript = `if redis.call("GET", KEYS[1]) ~= ARGV[1] then return 0 end redis.call("SET", KEYS[1], ARGV[2], "PX", ARGV[3]); return 1`
	n, err := cli.Eval(ctx, rotateScript, []string{key}, hashToken(oldToken), hashToken(newToken), ttl.Milliseconds()).Int()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// RemoveRefreshWhitelist 删除主体的 refresh token（登出/强制下线）
// 优先删 Redis; Redis 故障时回退删除应用内缓存
func RemoveRefreshWhitelist(ctx context.Context, identifier string) error {
	key := prefixRefreshWhitelist + identifier
	cli, err := authRedis()
	if err != nil {
		return err
	}
	return cli.Del(ctx, key).Err()
}

// hashToken 对 token 做 SHA256，避免在 Redis 中存储完整 JWT。
func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
