// markdown.go — 文章实体缓存 (markdown 业务)
//
// 查询键: 按对外对象 ID (markdown_id, UUID) 双表读取:
//   - 元信息 (markdowns): 列表/详情常用字段 (标题/摘要/可见性/作者)
//   - 正文 (markdown_contents): 详情页完整内容
//
// 上传/更新/删除后调用 Evict 失效, 保证下次读取回源新数据。
package store

import (
	"context"
	"fmt"

	basecache "gin-backend/internal/common/base/cache"
	dtocache "gin-backend/internal/model/cache"
	markdownmodel "gin-backend/internal/model/orm/markdown"

	"gorm.io/gorm"
)

// 文章缓存键命名空间 (按对外 markdown_id, 与 HTTP 路径参数一致)
const (
	markdownKeyMeta    = "cache:markdown:meta:%s"
	markdownKeyContent = "cache:markdown:content:%s"
)

// MarkdownStore 文章实体缓存存储器
type MarkdownStore struct {
	meta    *basecache.EntityCache[dtocache.MarkdownCache]
	content *basecache.EntityCache[dtocache.ContentCache]
}

// Markdown 文章实体缓存包级单例
var Markdown = &MarkdownStore{
	meta:    NewEntity[dtocache.MarkdownCache](DefaultTTL),
	content: NewEntity[dtocache.ContentCache](DefaultTTL),
}

// GetMeta 按 markdown_id 读取文章元信息 (透传 gorm.ErrRecordNotFound)
func (s *MarkdownStore) GetMeta(ctx context.Context, markdownID string) (*dtocache.MarkdownCache, error) {
	key := fmt.Sprintf(markdownKeyMeta, markdownID)
	m, err := s.meta.Get(ctx, key, func(ctx context.Context) (dtocache.MarkdownCache, error) {
		db, err := markdownDB.get(ctx)
		if err != nil {
			return dtocache.MarkdownCache{}, err
		}
		var row markdownmodel.Markdown
		if err := db.Where("markdown_id = ?", markdownID).First(&row).Error; err != nil {
			return dtocache.MarkdownCache{}, err
		}
		return *dtocache.FromMarkdown(&row), nil
	})
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// GetContent 按 markdown_id 读取文章正文 (透传 gorm.ErrRecordNotFound)
func (s *MarkdownStore) GetContent(ctx context.Context, markdownID string) (*dtocache.ContentCache, error) {
	key := fmt.Sprintf(markdownKeyContent, markdownID)
	c, err := s.content.Get(ctx, key, func(ctx context.Context) (dtocache.ContentCache, error) {
		db, err := markdownDB.get(ctx)
		if err != nil {
			return dtocache.ContentCache{}, err
		}
		var row markdownmodel.Content
		if err := db.Where("markdown_id = ?", markdownID).First(&row).Error; err != nil {
			return dtocache.ContentCache{}, err
		}
		return *dtocache.FromContent(&row), nil
	})
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// Evict 失效文章缓存 (元信息 + 正文)
func (s *MarkdownStore) Evict(ctx context.Context, markdownID string) error {
	_ = s.meta.Evict(ctx, fmt.Sprintf(markdownKeyMeta, markdownID))
	_ = s.content.Evict(ctx, fmt.Sprintf(markdownKeyContent, markdownID))
	return nil
}

// 编译期断言: MarkdownStore 依赖的 gorm 错误由调用方经 errors.Is 判断
var _ = gorm.ErrRecordNotFound
