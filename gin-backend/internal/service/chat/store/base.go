// Package store 定义聊天业务持久化存储层: 接口 + gorm 实现
// bridge 仅依赖本包接口 (可替换实现 / 可降级), 具体落库由 gorm 实现承担
package store

import (
	"sync"

	"gin-backend/internal/common/base/connection"
	postgresqlconn "gin-backend/internal/common/base/connection/postgresql"

	"gorm.io/gorm"
)

// gormStore 公共 gorm 存储基座: 维护 DB 连接懒加载
// 首次使用时经 PostgreSQLManager 获取 (复用 ServiceMarkdown 连接), 失败后缓存错误并降级
type gormStore struct {
	once sync.Once
	db   *gorm.DB
	err  error
}

// dbConn 懒加载获取底层连接 (首次调用时经 PostgreSQLManager 获取)
func (s *gormStore) dbConn() (*gorm.DB, error) {
	s.once.Do(func() {
		conn, err := postgresqlconn.PostgreSQLManager.Get(connection.ServiceMarkdown)
		if err != nil {
			s.err = err
			return
		}
		s.db, s.err = conn.GetConn()
	})
	return s.db, s.err
}
