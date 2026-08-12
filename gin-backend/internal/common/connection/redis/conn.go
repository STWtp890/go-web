package redis

import (
	"sync"

	"gin-backend/internal/config/must"

	goredis "github.com/redis/go-redis/v9"
)

// Conn Redis 连接封装对象
// 初始化由 sync.Once 保证只执行一次; 经 GetConn 获取底层连接
type Conn struct {
	once   sync.Once
	cfg    *must.RedisConfig
	client *goredis.Client
	err    error
}

// newConn 创建连接封装对象 (初始化延迟至注册或首次 GetConn 时执行)
func newConn(cfg *must.RedisConfig) *Conn {
	return &Conn{cfg: cfg}
}

// init 执行连接初始化 (仅由 once 调用一次)
func (c *Conn) init() {
	c.client, c.err = NewClient(c.cfg)
}

// GetConn 获取底层连接; 首次调用触发初始化 (由 once 保证只执行一次)
func (c *Conn) GetConn() (*goredis.Client, error) {
	c.once.Do(c.init)
	return c.client, c.err
}

// Close 关闭连接 (未初始化时为无操作)
func (c *Conn) Close() error {
	if c.client != nil {
		return c.client.Close()
	}
	return nil
}
