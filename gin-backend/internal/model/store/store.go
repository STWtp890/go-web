// Package store 业务实体缓存存储层
//
// 为各业务模块 (auth/manager/markdown) 提供实体级缓存读写:
//   - 主存储: Redis (cache 专用连接 ServiceCache)
//   - 回退存储: 进程内 MemCache (Redis 不可用时降级)
//   - 防击穿: singleflight 单飞合并同 key 的 DB 回源
//   - 一致性: 业务写库后调用 Evict 失效缓存 (Cache-Aside)
//
// 缓存 DTO 定义于上层包 internal/model/cache (字段齐全, 含 json:"-" 敏感字段),
// 与 HTTP 响应 JSON tag 解耦, 避免序列化丢字段。
package store

import (
	"context"
	"sync"
	"time"

	basecache "gin-backend/internal/common/base/cache"
	"gin-backend/internal/common/base/connection"
	postgresqlconn "gin-backend/internal/common/base/connection/postgresql"

	"gorm.io/gorm"
)

// DefaultTTL 实体缓存默认过期时间 (无参构造时使用)
const DefaultTTL = 15 * time.Minute

// 包级存储组件: Redis 主存储 + 进程内内存回退 (全局共享)
var (
	redisCache  = basecache.NewRedisCache()
	memoryCache = basecache.NewMemCache()
)

// NewEntity 便捷构造实体缓存器: 绑定包级 Redis/内存存储
// :Param
// - `ttl` 过期时间 (<= 0 时使用 DefaultTTL)
func NewEntity[T any](ttl time.Duration) *basecache.EntityCache[T] {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return basecache.NewEntityCache[T](redisCache, memoryCache, ttl)
}

// gormDB gorm 连接懒加载封装 (复用 PostgreSQLManager 注册的连接)
// 首次使用才获取底层连接; 获取失败缓存错误, 由调用方透传
type gormDB struct {
	once sync.Once
	name string // 注册名: ServiceAuth / ServiceMarkdown
	db   *gorm.DB
	err  error
}

// get 获取带 ctx 的连接 (懒加载)
func (g *gormDB) get(ctx context.Context) (*gorm.DB, error) {
	g.once.Do(func() {
		conn, err := postgresqlconn.PostgreSQLManager.Get(g.name)
		if err != nil {
			g.err = err
			return
		}
		g.db, g.err = conn.GetConn()
	})
	if g.err != nil {
		return nil, g.err
	}
	return g.db.WithContext(ctx), nil
}

// authDB 用户/管理员表连接 (ServiceAuth)
var authDB = &gormDB{name: connection.ServiceAuth}

// markdownDB 文章元信息/内容表连接 (ServiceMarkdown)
var markdownDB = &gormDB{name: connection.ServiceMarkdown}
