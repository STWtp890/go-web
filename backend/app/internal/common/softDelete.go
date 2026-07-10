package common

import (
	"context"
	"fmt"

	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

// SoftDelete 对指定表执行软删除：SET deleted_at = NOW()
// 使用 sqlx.SqlConn 以利用 go-zero 的连接池。
// table 应为转义后的表名，如 "`announcements`"。
func SoftDelete(ctx context.Context, conn sqlx.SqlConn, table string, id int64) error {
	query := fmt.Sprintf("UPDATE %s SET deleted_at = NOW() WHERE id = ? AND deleted_at IS NULL", table)
	result, err := conn.ExecCtx(ctx, query, id)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("record not found or already deleted: id=%d", id)
	}
	return nil
}
