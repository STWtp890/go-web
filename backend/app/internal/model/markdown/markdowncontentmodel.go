package markdown

import (
	"github.com/zeromicro/go-zero/core/stores/cache"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

var _ MarkdownContentModel = (*customMarkdownContentModel)(nil)

type (
	// MarkdownContentModel is an interface to be customized, add more methods here,
	// and implement the added methods in customMarkdownContentModel.
	MarkdownContentModel interface {
		markdownContentModel
	}

	customMarkdownContentModel struct {
		*defaultMarkdownContentModel
	}
)

// NewMarkdownContentModel returns a model for the database table.
func NewMarkdownContentModel(conn sqlx.SqlConn, c cache.CacheConf, opts ...cache.Option) MarkdownContentModel {
	return &customMarkdownContentModel{
		defaultMarkdownContentModel: newMarkdownContentModel(conn, c, opts...),
	}
}
