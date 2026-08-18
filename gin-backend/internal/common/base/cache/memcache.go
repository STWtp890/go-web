// memstore.go — 进程内内存键值缓存存储 (MemCache 适配 Store 接口)
//
// 用途: 实体缓存的回退存储 (Redis 不可用/故障时降级), 单进程内共享。
// 与 RedisStore 组合: 主存储 Redis 未命中或故障时查内存, 命中后回填 Redis。
package cache

import (
	"context"
	"errors"
	"sync"
	"time"
)

// cacheEntry 缓存条目
type cacheEntry struct {
	val   string
	expAt time.Time // 零值表示不过期
}

// MemCache 基本应用内缓存 (线程安全, 惰性过期)
type MemCache struct {
	mu   sync.RWMutex
	data map[string]cacheEntry
}

// NewMemCache 创建应用内缓存
func NewMemCache() *MemCache {
	return &MemCache{data: make(map[string]cacheEntry)}
}

// Get 读取缓存值; 不存在或已过期返回 ok=false (过期条目惰性清理, 不阻塞读)
func (c *MemCache) Get(ctx context.Context, key string) (string, error) {
	c.mu.RLock()
	e, exists := c.data[key]
	c.mu.RUnlock()
	if !exists {
		return "", errors.New("cache miss")
	}
	// 已过期: 惰性删除并视为未命中
	if !e.expAt.IsZero() && time.Now().After(e.expAt) {
		c.Del(ctx, key)
		return "", errors.New("cache miss")
	}
	return e.val, nil
}

// Set 写入缓存并设置 TTL; ttl <= 0 表示不过期
func (c *MemCache) Set(ctx context.Context, key, val string, ttl time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var expAt time.Time
	if ttl > 0 {
		expAt = time.Now().Add(ttl)
	}
	c.data[key] = cacheEntry{val: val, expAt: expAt}
	return nil
}

// Delete 删除缓存条目 (幂等)
func (c *MemCache) Del(ctx context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.data, key)
	return nil
}

// Len 返回当前缓存条目数 (含已过期未清理条目, 调试用)
func (c *MemCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.data)
}
