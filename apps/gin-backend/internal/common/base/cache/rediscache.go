// redisstore.go — Redis 键值缓存存储 (跨进程共享, 实体缓存主存储)
//
// 连接懒加载: 首次操作时经 RedisManager 获取 cache 专用连接 (ServiceCache),
// 获取失败后缓存错误 (后续操作直接返回, 不再重试注册), 由上层降级到内存回退。
package cache

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"gin-backend/internal/common/base/connection"
	redisconn "gin-backend/internal/common/base/connection/redis"

	goredis "github.com/redis/go-redis/v9"
)

// RedisCache Store 的 Redis 实现
type RedisCache struct {
	once sync.Once
	cli  *goredis.Client
	err  error
}

// NewRedisCache 创建 Redis 缓存存储 (懒加载 cache 专用连接)
func NewRedisCache() *RedisCache {
	return &RedisCache{}
}

// client 懒加载获取 Redis 连接 (仅首次真实获取, 失败缓存错误)
func (s *RedisCache) client() (*goredis.Client, error) {
	s.once.Do(func() {
		conn, err := redisconn.RedisManager.Get(connection.ServiceCache)
		if err != nil {
			s.err = fmt.Errorf("cache/redis: 获取连接失败: %w", err)
			return
		}
		s.cli, s.err = conn.GetConn()
	})
	return s.cli, s.err
}

// Get 读取 key; 未命中返回 ErrMiss
func (s *RedisCache) Get(ctx context.Context, key string) (string, error) {
	cli, err := s.client()
	if err != nil {
		return "", err
	}
	val, err := cli.Get(ctx, key).Result()
	if errors.Is(err, goredis.Nil) {
		return "", ErrMiss
	}
	if err != nil {
		return "", err
	}
	return val, nil
}

// Set 写入 key=val 并设置 TTL (ttl <= 0 不过期)
func (s *RedisCache) Set(ctx context.Context, key string, val string, ttl time.Duration) error {
	cli, err := s.client()
	if err != nil {
		return err
	}
	return cli.Set(ctx, key, val, ttl).Err()
}

// Del 删除 key (幂等)
func (s *RedisCache) Del(ctx context.Context, key string) error {
	cli, err := s.client()
	if err != nil {
		return err
	}
	return cli.Del(ctx, key).Err()
}
