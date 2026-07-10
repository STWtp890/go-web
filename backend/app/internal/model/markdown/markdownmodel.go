package markdown

import (
	"github.com/zeromicro/go-zero/core/stores/cache"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

var _ MarkdownModel = (*customMarkdownModel)(nil)

type (
	// MarkdownModel is an interface to be customized, add more methods here,
	// and implement the added methods in customMarkdownModel.
	MarkdownModel interface {
		markdownModel
	}

	customMarkdownModel struct {
		*defaultMarkdownModel
	}
)

// NewMarkdownModel returns a model for the database table.
func NewMarkdownModel(conn sqlx.SqlConn, c cache.CacheConf, opts ...cache.Option) MarkdownModel {
	return &customMarkdownModel{
		defaultMarkdownModel: newMarkdownModel(conn, c, opts...),
	}
}
