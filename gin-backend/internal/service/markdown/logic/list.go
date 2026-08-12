package logic

import (
	"context"

	markdownmodel "gin-backend/internal/orm/markdown"
)

// ListMyMarkdownLogic 当前用户的文章分页列表 (不含完整 content, 列表用 summary)
// :Param
// - `ctx` 上下文
// - `authorID` 当前登录用户标识 (JWT sub)
// - `page` 页码 (<=0 时默认 1)
// - `pageSize` 每页条数 (<=0 或 >100 时默认 10)
// :Return
// - `[]*markdownmodel.Markdown` 文章元信息列表 (按 id 倒序)
// - `int64` 该用户文章总数 (用于分页)
// - `error` 如果查询失败, 返回错误信息
func ListMyMarkdownLogic(ctx context.Context, authorID string, page, pageSize int) ([]*markdownmodel.Markdown, int64, error) {
	db, err := markdownDB(ctx)
	if err != nil {
		return nil, 0, err
	}

	// 1. 归一化分页参数
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 10
	}

	// 2. 统计总数 (软删除过滤由 gorm 自动附加: deleted_at IS NULL)
	query := db.Model(&markdownmodel.Markdown{}).
		Where("author_id = ?", authorID)

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// 3. 分页查询列表
	var list []*markdownmodel.Markdown
	if err := db.Where("author_id = ?", authorID).
		Order("id DESC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&list).Error; err != nil {
		return nil, 0, err
	}

	return list, total, nil
}
