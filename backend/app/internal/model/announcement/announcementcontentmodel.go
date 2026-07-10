package announcement

import (
	"github.com/zeromicro/go-zero/core/stores/cache"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

var _ AnnouncementContentModel = (*customAnnouncementContentModel)(nil)

type (
	// AnnouncementContentModel is an interface to be customized, add more methods here,
	// and implement the added methods in customAnnouncementContentModel.
	AnnouncementContentModel interface {
		announcementContentModel
	}

	customAnnouncementContentModel struct {
		*defaultAnnouncementContentModel
	}
)

// NewAnnouncementContentModel returns a model for the database table.
func NewAnnouncementContentModel(conn sqlx.SqlConn, c cache.CacheConf, opts ...cache.Option) AnnouncementContentModel {
	return &customAnnouncementContentModel{
		defaultAnnouncementContentModel: newAnnouncementContentModel(conn, c, opts...),
	}
}
