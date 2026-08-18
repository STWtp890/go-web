// store.go — 通用键值缓存存储接口与错误定义
//
// 缓存存储抽象: 将"存哪" (Redis / 进程内 MemCache) 与"存什么" (实体缓存) 解耦。
// 实体缓存器 (entity.go) 依赖本接口做分层读写; 上层可自由组合主存储与回退存储。
package cache

import (
	"context"
	"time"
)

// Store 键值缓存存储接口
// 值统一为 string: 上层 (EntityCache) 负责序列化 (JSON) 与反序列化。
// :Implement
// - `*RedisStore` 基于 go-redis (跨进程共享, 主存储)
// - `*MemStore` 基于进程内 MemCache (单进程, 回退/降级)
type Store interface {
	// Get 读取 key 的值; 未命中返回 ErrMiss, 存储故障返回其他错误
	Get(ctx context.Context, key string) (string, error)
	// Set 写入 key=val, ttl 过期时间 (ttl <= 0 表示不过期)
	Set(ctx context.Context, key string, val string, ttl time.Duration) error
	// Del 删除 key (幂等: key 不存在不视为错误)
	Del(ctx context.Context, key string) error
}

type AsyncStore interface {
	// AGet 异步读取 key 的值; 未命中返回 ErrMiss, 存储故障返回其他错误
	AGet(ctx context.Context, key string) (string, error)
	// ASet 异步写入 key=val, ttl 过期时间 (ttl <= 0 表示不过期)
	ASet(ctx context.Context, key string, val string, ttl time.Duration) error
	// ADel 异步删除 key (幂等: key 不存在不视为错误)
	ADel(ctx context.Context, key string) error
}
