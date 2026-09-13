package utils

import (
	"context"
	"fmt"

	"gin-backend/internal/common/base/connection"
	postgresqlconn "gin-backend/internal/common/base/connection/postgresql"

	"gorm.io/gorm"
)

// ManagerDB 获取 manager 业务专属的 PostgreSQL 连接 (复用 ServiceAuth, 带 ctx)
func ManagerDB(ctx context.Context) (*gorm.DB, error) {
	conn, err := postgresqlconn.PostgreSQLManager.Get(connection.ServiceAuth)
	if err != nil {
		return nil, fmt.Errorf("postgresql: 获取连接失败: %w", err)
	}
	db, err := conn.GetConn()
	if err != nil {
		return nil, fmt.Errorf("postgresql: 获取底层连接失败: %w", err)
	}
	return db.WithContext(ctx), nil
}
