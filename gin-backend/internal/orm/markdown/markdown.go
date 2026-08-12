package markdown

import "gin-backend/internal/orm"

// Markdown 文章元信息模型
// :Field
// - MarkdownID: 对象ID (UUIDv7 风格 UUID), 对外暴露防遍历攻击
// - AuthorID: 作者标识 (JWT sub, 即 users.id)
type Markdown struct {
	ID           uint   `json:"id" gorm:"primaryKey;autoIncrement"`
	MarkdownID   string `json:"markdown_id" gorm:"size:64;uniqueIndex;not null"`
	AuthorUserID string `json:"author_id" gorm:"size:64;index;not null"`
	Title        string `json:"title" gorm:"size:255;not null"`
	Summary      string `json:"summary" gorm:"size:512;default:''"`
	SearchText   string `json:"-" gorm:"type:text;not null;default:''"`
	orm.TimeFiled
}

// TableName 指定表名
func (*Markdown) TableName() string {
	return "markdowns"
}

// Content 文章内容模型 (1:1 关联 markdown.MarkdownID)
type Content struct {
	ID         uint   `json:"id" gorm:"primaryKey;autoIncrement"`
	MarkdownID string `json:"markdown_id" gorm:"size:64;uniqueIndex;not null"`
	Content    string `json:"content" gorm:"type:text;not null"`
}

// TableName 指定表名
func (*Content) TableName() string {
	return "markdown_contents"
}
