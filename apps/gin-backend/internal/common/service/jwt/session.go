// Package jwt 的会话状态管理。
//
// Access Token 保持无状态签名，但每个 token 均携带 sid。Redis 只保存一个账号
// 当前有效 sid，因此新登录覆盖 sid 后，旧 Access/Refresh Token 都会在下次使用时
// 立即失效。
package jwt

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"gin-backend/internal/common/base/connection"
	redisconn "gin-backend/internal/common/base/connection/redis"

	goredis "github.com/redis/go-redis/v9"
)

const (
	principalUser    = "user"
	principalManager = "manager"
	prefixSession    = "jwt:session:"
	fieldSID         = "sid"
	fieldRefresh     = "refresh_hash"
)

var (
	errInvalidSessionScope = errors.New("无效的会话主体类型")
	errInvalidSessionInput = errors.New("无效的会话参数")
)

// NewSessionID 生成一个 256 位随机、URL 安全的会话标识。
func NewSessionID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("生成会话标识失败: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// CreateOrReplaceUserSession 创建或覆盖用户的当前有效会话。
// 返回被覆盖的旧 sid；首次登录时返回空字符串。状态替换在 Redis 内原子完成。
func CreateOrReplaceUserSession(ctx context.Context, userID, sid, refreshToken string, ttl time.Duration) (string, error) {
	return createOrReplaceSession(ctx, principalUser, userID, sid, refreshToken, ttl)
}

// CreateOrReplaceManagerSession 创建或覆盖管理员的当前有效会话。
func CreateOrReplaceManagerSession(ctx context.Context, managerID, sid, refreshToken string, ttl time.Duration) (string, error) {
	return createOrReplaceSession(ctx, principalManager, managerID, sid, refreshToken, ttl)
}

func createOrReplaceSession(ctx context.Context, principalType, principalID, sid, refreshToken string, ttl time.Duration) (string, error) {
	key, err := sessionKey(principalType, principalID)
	if err != nil {
		return "", err
	}
	if sid == "" || refreshToken == "" || ttl <= 0 {
		return "", errInvalidSessionInput
	}

	cli, err := authRedis()
	if err != nil {
		return "", err
	}

	const script = `
local oldSID = redis.call("HGET", KEYS[1], ARGV[1])
redis.call("HSET", KEYS[1], ARGV[1], ARGV[2], ARGV[3], ARGV[4])
redis.call("PEXPIRE", KEYS[1], ARGV[5])
return oldSID or ""
`
	result, err := cli.Eval(ctx, script, []string{key}, fieldSID, sid, fieldRefresh, hashToken(refreshToken), ttl.Milliseconds()).Result()
	if err != nil {
		return "", err
	}
	oldSID, ok := result.(string)
	if !ok {
		return "", fmt.Errorf("创建会话返回值无效: %T", result)
	}
	return oldSID, nil
}

// IsUserSessionActive 判断 sid 是否仍是用户的当前有效会话。
func IsUserSessionActive(ctx context.Context, userID, sid string) (bool, error) {
	return isSessionActive(ctx, principalUser, userID, sid)
}

// IsManagerSessionActive 判断 sid 是否仍是管理员的当前有效会话。
func IsManagerSessionActive(ctx context.Context, managerID, sid string) (bool, error) {
	return isSessionActive(ctx, principalManager, managerID, sid)
}

func isSessionActive(ctx context.Context, principalType, principalID, sid string) (bool, error) {
	key, err := sessionKey(principalType, principalID)
	if err != nil {
		return false, err
	}
	if sid == "" {
		return false, nil
	}

	cli, err := authRedis()
	if err != nil {
		return false, err
	}
	storedSID, err := cli.HGet(ctx, key, fieldSID).Result()
	if err == goredis.Nil {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return storedSID == sid, nil
}

// RotateUserRefreshToken 原子校验并轮换当前会话的 Refresh Token。
// 返回 false 表示会话已被替换、已撤销，或旧 Refresh Token 已被消费。
func RotateUserRefreshToken(ctx context.Context, userID, sid, oldToken, newToken string, ttl time.Duration) (bool, error) {
	return rotateRefreshToken(ctx, principalUser, userID, sid, oldToken, newToken, ttl)
}

// RotateManagerRefreshToken 原子校验并轮换管理员当前会话的 Refresh Token。
func RotateManagerRefreshToken(ctx context.Context, managerID, sid, oldToken, newToken string, ttl time.Duration) (bool, error) {
	return rotateRefreshToken(ctx, principalManager, managerID, sid, oldToken, newToken, ttl)
}

func rotateRefreshToken(ctx context.Context, principalType, principalID, sid, oldToken, newToken string, ttl time.Duration) (bool, error) {
	key, err := sessionKey(principalType, principalID)
	if err != nil {
		return false, err
	}
	if sid == "" || oldToken == "" || newToken == "" || ttl <= 0 {
		return false, errInvalidSessionInput
	}

	cli, err := authRedis()
	if err != nil {
		return false, err
	}

	const script = `
if redis.call("HGET", KEYS[1], ARGV[1]) ~= ARGV[2] then return 0 end
if redis.call("HGET", KEYS[1], ARGV[3]) ~= ARGV[4] then return 0 end
redis.call("HSET", KEYS[1], ARGV[3], ARGV[5])
redis.call("PEXPIRE", KEYS[1], ARGV[6])
return 1
`
	n, err := cli.Eval(ctx, script, []string{key}, fieldSID, sid, fieldRefresh, hashToken(oldToken), hashToken(newToken), ttl.Milliseconds()).Int()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// RevokeUserSessionIfCurrent 删除 sid 对应的当前会话。条件删除可防止旧端的延迟
// 登出请求误删新端刚建立的会话。
func RevokeUserSessionIfCurrent(ctx context.Context, userID, sid string) (bool, error) {
	return revokeSessionIfCurrent(ctx, principalUser, userID, sid)
}

// RevokeManagerSessionIfCurrent 条件删除管理员当前 sid 会话。
func RevokeManagerSessionIfCurrent(ctx context.Context, managerID, sid string) (bool, error) {
	return revokeSessionIfCurrent(ctx, principalManager, managerID, sid)
}

// RevokeManagerSession 删除管理员的当前会话，供未来受授权的目标管理员强制下线、
// 停用账号等流程调用。
func RevokeManagerSession(ctx context.Context, managerID string) error {
	key, err := sessionKey(principalManager, managerID)
	if err != nil {
		return err
	}
	cli, err := authRedis()
	if err != nil {
		return err
	}
	return cli.Del(ctx, key).Err()
}

func revokeSessionIfCurrent(ctx context.Context, principalType, principalID, sid string) (bool, error) {
	key, err := sessionKey(principalType, principalID)
	if err != nil {
		return false, err
	}
	if sid == "" {
		return false, nil
	}

	cli, err := authRedis()
	if err != nil {
		return false, err
	}

	const script = `
if redis.call("HGET", KEYS[1], ARGV[1]) ~= ARGV[2] then return 0 end
redis.call("DEL", KEYS[1])
return 1
`
	n, err := cli.Eval(ctx, script, []string{key}, fieldSID, sid).Int()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

func sessionKey(principalType, principalID string) (string, error) {
	if principalType != principalUser && principalType != principalManager {
		return "", errInvalidSessionScope
	}
	if principalID == "" {
		return "", errInvalidSessionInput
	}
	return prefixSession + principalType + ":" + principalID, nil
}

func authRedis() (*goredis.Client, error) {
	conn, err := redisconn.RedisManager.Get(connection.ServiceAuth)
	if err != nil {
		return nil, fmt.Errorf("jwt redis 不可用: %w", err)
	}
	return conn.GetConn()
}
