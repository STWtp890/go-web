// entity.go — 泛型实体缓存器: 分层读写 + singleflight 防击穿 + DB 回源
//
// 缓存模式: Cache-Aside (旁路缓存)。
//
//	Get  : 主存储(Redis) → 未命中/故障 → [singleflight 单飞] 内存回退 → DB 回源 → 回填两级
//	Evict: 写库后调用, 删除两级缓存 (保证一致性)
//
// 并发防护: 同一 key 的并发 Get 经 singleflight.Group 合并为一次 DB 回源,
// 高并发热点 (如登录查用户) 不会击穿到数据库。
package cache

import (
	"context"
	"encoding/json"
	"time"

	"golang.org/x/sync/singleflight"
)

// Loader DB 回源函数: 缓存未命中时从持久层加载实体
// 约定: 未找到应返回哨兵错误 (由业务层定义), 调用方据此区分"不存在"与"系统故障"
type Loader[T any] func(ctx context.Context) (T, error)

// EntityCache 泛型实体缓存器 (线程安全)
// :Field
// - `primary` 主存储 (Redis, 跨进程共享)
// - `fallback` 回退存储 (进程内 MemCache)
// - `ttl` 缓存过期时间
// - `group` singleflight 单飞组件 (防击穿)
type EntityCache[T any] struct {
	primary  Store
	fallback Store
	ttl      time.Duration
	group    singleflight.Group
}

// NewEntityCache 创建实体缓存器
// :Param
// - `primary` 主存储 (通常为 NewRedisCache())
// - `fallback` 回退存储 (通常为 NewMemCache())
// - `ttl` 缓存过期时间 (<= 0 不过期)
func NewEntityCache[T any](primary, fallback Store, ttl time.Duration) *EntityCache[T] {
	return &EntityCache[T]{
		primary:  primary,
		fallback: fallback,
		ttl:      ttl,
	}
}

// Get 读取实体: 主存储 → (单飞) 内存回退 → DB 回源 → 回填
// :Param
// - `ctx` 上下文
// - `key` 缓存键 (业务层负责命名空间前缀, 如 "user:id:1")
// - `load` DB 回源函数 (仅缓存未命中时调用)
// :Return
// - `T` 实体值
// - `error` ErrMiss 之外的回源错误 (load 的返回值原样透传)
func (e *EntityCache[T]) Get(ctx context.Context, key string, load Loader[T]) (T, error) {
	var zero T

	// 1. Redis 命中直接返回
	if raw, err := e.primary.Get(ctx, key); err == nil {
		return decode[T](raw)
	} else if err != ErrMiss {
		// 主存储故障 (连接不可用等) → 降级走回源路径, 内部先查内存
	}

	// 2. 单飞回源: 同 key 并发只放行一个 (防击穿 / 合并回源)
	v, err, _ := e.group.Do(key, func() (any, error) {
		// 2a. 内存回退: 命中直接返回, 并尽力回填主存储
		if raw, err := e.fallback.Get(ctx, key); err == nil {
			_ = e.primary.Set(ctx, key, raw, e.ttl) // 回填失败不影响读
			return decode[T](raw)
		} else if err != ErrMiss {
			// 内存也故障 → 直接 DB
		}

		// 2b. DB 回源
		t, err := load(ctx)
		if err != nil {
			return zero, err // 回源失败不缓存 (错误透传, 下次重试)
		}

		raw, err := json.Marshal(&t)
		if err != nil {
			return zero, err
		}
		// 回填两级缓存 (尽力而为)
		_ = e.primary.Set(ctx, key, string(raw), e.ttl)
		_ = e.fallback.Set(ctx, key, string(raw), e.ttl)
		return t, nil
	})
	if err != nil {
		return zero, err
	}
	return v.(T), nil
}

// Evict 失效实体缓存 (两级删除; 幂等)
// 业务层在写库后调用, 保证下次读取回源新数据 (Cache-Aside 一致性)
func (e *EntityCache[T]) Evict(ctx context.Context, key string) error {
	_ = e.primary.Del(ctx, key)
	_ = e.fallback.Del(ctx, key)
	return nil
}

// decode 反序列化缓存值
func decode[T any](raw string) (T, error) {
	var t T
	if err := json.Unmarshal([]byte(raw), &t); err != nil {
		return t, err
	}
	return t, nil
}
