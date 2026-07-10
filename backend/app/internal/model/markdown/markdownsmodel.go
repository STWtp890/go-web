package markdown

import (
	"github.com/zeromicro/go-zero/core/stores/cache"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

var _ MarkdownsModel = (*customMarkdownsModel)(nil)

type (
	// MarkdownsModel is an interface to be customized, add more methods here,
	// and implement the added methods in customMarkdownsModel.
	MarkdownsModel interface {
		markdownsModel
	}

	customMarkdownsModel struct {
		*defaultMarkdownsModel
	}
)

// NewMarkdownsModel returns a model for the database table.
func NewMarkdownsModel(conn sqlx.SqlConn, c cache.CacheConf, opts ...cache.Option) MarkdownsModel {
	return &customMarkdownsModel{
		defaultMarkdownsModel: newMarkdownsModel(conn, c, opts...),
	}
}
