package announcement

import (
	"github.com/zeromicro/go-zero/core/stores/cache"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

var _ AnnouncementModel = (*customAnnouncementModel)(nil)

type (
	// AnnouncementModel is an interface to be customized, add more methods here,
	// and implement the added methods in customAnnouncementModel.
	AnnouncementModel interface {
		announcementModel
	}

	customAnnouncementModel struct {
		*defaultAnnouncementModel
	}
)

// NewAnnouncementModel returns a model for the database table.
func NewAnnouncementModel(conn sqlx.SqlConn, c cache.CacheConf, opts ...cache.Option) AnnouncementModel {
	return &customAnnouncementModel{
		defaultAnnouncementModel: newAnnouncementModel(conn, c, opts...),
	}
}
