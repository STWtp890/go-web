// markdown 实体缓存对象
package cache

import "gin-backend/internal/model/orm/markdown"

// MarkdownCache 文章元信息缓存对象 (字段齐全, 含 SearchText)
type MarkdownCache struct {
	ID         uint   `json:"id"`
	MarkdownID string `json:"markdown_id"`
	AuthorID   string `json:"author_id"`
	Title      string `json:"title"`
	Summary    string `json:"summary"`
	Visibility string `json:"visibility"`
	SearchText string `json:"search_text"` // 全文检索索引字段, 缓存需要
	CreatedAt  int64  `json:"created_at"`
	UpdatedAt  int64  `json:"updated_at"`
}

// FromMarkdown 由 ORM 文章元信息构造缓存对象
func FromMarkdown(m *markdown.Markdown) *MarkdownCache {
	return &MarkdownCache{
		ID:         m.ID,
		MarkdownID: m.MarkdownID,
		AuthorID:   m.AuthorID,
		Title:      m.Title,
		Summary:    m.Summary,
		Visibility: m.Visibility,
		SearchText: m.SearchText,
		CreatedAt:  m.CreatedAt,
		UpdatedAt:  m.UpdatedAt,
	}
}

// ContentCache 文章内容缓存对象
type ContentCache struct {
	ID         uint   `json:"id"`
	MarkdownID string `json:"markdown_id"`
	Content    string `json:"content"`
}

// FromContent 由 ORM 文章内容构造缓存对象
func FromContent(c *markdown.Content) *ContentCache {
	return &ContentCache{
		ID:         c.ID,
		MarkdownID: c.MarkdownID,
		Content:    c.Content,
	}
}
