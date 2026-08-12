package logic

import (
	"context"
	"fmt"
	"strings"

	"gin-backend/internal/common/connection"
	"gin-backend/internal/common/connection/postgresql"

	"gorm.io/gorm"
)

const (
	// summaryMaxLen summary 取正文前 N 个字符
	summaryMaxLen = 100
)

// markdownDB 获取 markdown 业务专属的 PostgreSQL 连接 (带 ctx)
// :Return
// - `*gorm.DB` 已就绪的连接 (context 已注入)
// - `error` 如果连接未注册或未初始化, 返回错误信息
func markdownDB(ctx context.Context) (*gorm.DB, error) {
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

// buildSummary 从正文生成摘要: 去除首尾空白后取前 summaryMaxLen 个字符
// :Param
// - `content` 完整 Markdown 正文
// :Return
// - `string` 摘要文本 (空正文返回空串)
func buildSummary(content string) string {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return ""
	}

	runes := []rune(trimmed)
	if len(runes) > summaryMaxLen {
		runes = runes[:summaryMaxLen]
	}
	return string(runes)
}
