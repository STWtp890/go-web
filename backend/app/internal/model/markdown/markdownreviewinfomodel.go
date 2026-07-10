package markdown

import (
	"github.com/zeromicro/go-zero/core/stores/cache"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

var _ MarkdownReviewInfoModel = (*customMarkdownReviewInfoModel)(nil)

type (
	// MarkdownReviewInfoModel is an interface to be customized, add more methods here,
	// and implement the added methods in customMarkdownReviewInfoModel.
	MarkdownReviewInfoModel interface {
		markdownReviewInfoModel
	}

	customMarkdownReviewInfoModel struct {
		*defaultMarkdownReviewInfoModel
	}
)

// NewMarkdownReviewInfoModel returns a model for the database table.
func NewMarkdownReviewInfoModel(conn sqlx.SqlConn, c cache.CacheConf, opts ...cache.Option) MarkdownReviewInfoModel {
	return &customMarkdownReviewInfoModel{
		defaultMarkdownReviewInfoModel: newMarkdownReviewInfoModel(conn, c, opts...),
	}
}
