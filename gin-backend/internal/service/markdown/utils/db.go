package utils

import (
	"context"
	"fmt"

	"gin-backend/internal/common/base/connection"
	"gin-backend/internal/common/base/connection/postgresql"

	"gorm.io/gorm"
)

// MarkdownDB 获取 markdown 业务专属的 PostgreSQL 连接 (带 ctx)
// :Return
// - `*gorm.DB` 已就绪的连接 (context 已注入)
// - `error` 如果连接未注册或未初始化, 返回错误信息
func MarkdownDB(ctx context.Context) (*gorm.DB, error) {
	conn, err := postgresql.PostgreSQLManager.Get(connection.ServiceMarkdown)
	if err != nil {
		return nil, fmt.Errorf("postgresql: 获取连接失败: %w", err)
	}
	db, err := conn.GetConn()
	if err != nil {
		return nil, fmt.Errorf("postgresql: 获取底层连接失败: %w", err)
	}
	return db.WithContext(ctx), nil
}
