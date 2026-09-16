// entity.go — 泛型实体缓存器: 分层读写 + singleflight 防击穿 + DB 回源
//
// 缓存模式: Cache-Aside (旁路缓存)。
//
//	Get  : 主存储(Redis) → 未命中/故障 → [singleflight 单飞] 内存回退 → DB 回源 → 回填两级
//	Evict: 删除两级缓存，负责旧键回收；可变实体的一致性由业务版本键保证
//
// 并发防护: 同进程、同命名空间、同 key 的并发 Get 合并为一次 DB 回源。
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	primary   Store
	fallback  Store
	ttl       time.Duration
	group     *singleflight.Group
	namespace string
	stats     *entityCounters
}

func newEntityCache[T any](
	primary Store,
	fallback Store,
	group *singleflight.Group,
	namespace string,
	stats *entityCounters,
	ttl time.Duration,
) *EntityCache[T] {
	return &EntityCache[T]{
		primary: primary, fallback: fallback, ttl: ttl, group: group,
		namespace: namespace, stats: stats,
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

	if raw, err := e.primary.Get(ctx, key); err == nil {
		e.stats.primaryHits.Add(1)
		return decode[T](raw)
	} else if errors.Is(err, ErrMiss) {
		e.stats.primaryMisses.Add(1)
	} else {
		e.stats.primaryErrors.Add(1)
	}

	v, err, shared := e.group.Do(e.namespace+"\x00"+key, func() (any, error) {
		if raw, err := e.fallback.Get(ctx, key); err == nil {
			e.stats.fallbackHits.Add(1)
			if fillErr := e.primary.Set(ctx, key, raw, e.ttl); fillErr != nil {
				e.stats.fillErrors.Add(1)
			}
			return raw, nil
		} else if errors.Is(err, ErrMiss) {
			e.stats.fallbackMisses.Add(1)
		} else {
			e.stats.fallbackErrors.Add(1)
		}

		e.stats.loads.Add(1)
		loaded, err := load(ctx)
		if err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(&loaded)
		if err != nil {
			return nil, err
		}
		raw := string(encoded)
		if fillErr := e.primary.Set(ctx, key, raw, e.ttl); fillErr != nil {
			e.stats.fillErrors.Add(1)
		}
		if fillErr := e.fallback.Set(ctx, key, raw, e.ttl); fillErr != nil {
			e.stats.fillErrors.Add(1)
		}
		return raw, nil
	})
	if shared {
		e.stats.shared.Add(1)
	}
	if err != nil {
		return zero, err
	}
	raw, ok := v.(string)
	if !ok {
		return zero, fmt.Errorf("cache %s: singleflight result type %T", e.namespace, v)
	}
	return decode[T](raw)
}

// Evict 删除实体缓存 (两级删除; 幂等)。
// 删除失败会聚合返回；调用方决定重试或记录。它本身不仲裁在途回填。
func (e *EntityCache[T]) Evict(ctx context.Context, key string) error {
	primaryErr := e.primary.Del(ctx, key)
	fallbackErr := e.fallback.Del(ctx, key)
	if primaryErr != nil || fallbackErr != nil {
		e.stats.evictErrors.Add(1)
	}
	return errors.Join(
		wrapCacheOperationError("primary delete", primaryErr),
		wrapCacheOperationError("fallback delete", fallbackErr),
	)
}

func wrapCacheOperationError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("cache %s: %w", operation, err)
}

// decode 反序列化缓存值
func decode[T any](raw string) (T, error) {
	var t T
	if err := json.Unmarshal([]byte(raw), &t); err != nil {
		return t, err
	}
	return t, nil
}
