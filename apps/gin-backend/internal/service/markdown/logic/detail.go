package logic

import (
	"context"
	"errors"

	"gin-backend/internal/model/orm"
	markdownmodel "gin-backend/internal/model/orm/markdown"
	"gin-backend/internal/model/store"

	"gorm.io/gorm"
)

// GetMarkdownLogic 获取文章详情 (元信息 + 完整 content)
// 可见性校验: public 任意登录用户可读; private 仅作者本人可读 (防越权 IDOR)
// :Param
// - `ctx` 上下文
// - `viewerID` 当前登录用户标识 (JWT sub)
// - `markdownID` 文章对象ID (UUID)
// :Return
// - `*markdownmodel.Markdown` 文章元信息
// - `string` 完整 Markdown 正文
// - `error` 文章不存在 / 无权查看 / 查询失败
func GetMarkdownLogic(ctx context.Context, viewerID, markdownID string) (*markdownmodel.Markdown, string, error) {
	if viewerID == "" {
		return nil, "", errors.New("无法识别用户身份")
	}

	// 1. 查询元信息 (实体缓存: Redis 主 → 内存回退 → DB 回源; 软删除过滤由 gorm 自动附加)
	mc, err := store.Markdown.GetMeta(ctx, markdownID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, "", ErrMarkdownNotFound
		}
		return nil, "", err
	}
	// 缓存 DTO → ORM 模型 (可见性校验与响应共用)
	md := &markdownmodel.Markdown{
		ID:         mc.ID,
		MarkdownID: mc.MarkdownID,
		AuthorID:   mc.AuthorID,
		Title:      mc.Title,
		Summary:    mc.Summary,
		Visibility: mc.Visibility,
		SearchText: mc.SearchText,
		TimeFiled: orm.TimeFiled{
			CreatedAt: mc.CreatedAt,
			UpdatedAt: mc.UpdatedAt,
		},
	}

	// 2. 可见性校验: private 仅作者; public 任意登录用户
	if md.Visibility == markdownmodel.VisibilityPrivate && md.AuthorID != viewerID {
		return nil, "", ErrMarkdownForbidden
	}

	// 3. 查询正文内容 (实体缓存)
	cc, err := store.Markdown.GetContent(ctx, markdownID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, "", ErrMarkdownNotFound
		}
		return nil, "", err
	}

	return md, cc.Content, nil
}
