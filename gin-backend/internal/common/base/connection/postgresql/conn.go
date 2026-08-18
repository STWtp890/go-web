package postgresql

import (
	"sync"

	"gin-backend/internal/config/must"

	"gorm.io/gorm"
)

// Conn PostgreSQL 连接封装对象
// 初始化由 sync.Once 保证只执行一次; 经 GetConn 获取底层连接
type Conn struct {
	once     sync.Once
	cfg      must.PostgresConfig
	logLevel string
	db       *gorm.DB
	err      error
}

// newConn 创建连接封装对象 (初始化延迟至注册或首次 GetConn 时执行)
func newConn(cfg must.PostgresConfig, logLevel string) *Conn {
	return &Conn{cfg: cfg, logLevel: logLevel}
}

// init 执行连接初始化 (仅由 once 调用一次)
func (c *Conn) init() {
	c.db, c.err = NewDB(c.cfg, c.logLevel)
}

// GetConn 获取底层连接; 首次调用触发初始化 (由 once 保证只执行一次)
func (c *Conn) GetConn() (*gorm.DB, error) {
	c.once.Do(c.init)
	return c.db, c.err
}

// Close 关闭连接 (未初始化时为无操作)
func (c *Conn) Close() error {
	if c.db != nil {
		return Close(c.db)
	}
	return nil
}
