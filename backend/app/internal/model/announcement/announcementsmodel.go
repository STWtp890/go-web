package announcement

import (
	"github.com/zeromicro/go-zero/core/stores/cache"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

var _ AnnouncementsModel = (*customAnnouncementsModel)(nil)

type (
	// AnnouncementsModel is an interface to be customized, add more methods here,
	// and implement the added methods in customAnnouncementsModel.
	AnnouncementsModel interface {
		announcementsModel
	}

	customAnnouncementsModel struct {
		*defaultAnnouncementsModel
	}
)

// NewAnnouncementsModel returns a model for the database table.
func NewAnnouncementsModel(conn sqlx.SqlConn, c cache.CacheConf, opts ...cache.Option) AnnouncementsModel {
	return &customAnnouncementsModel{
		defaultAnnouncementsModel: newAnnouncementsModel(conn, c, opts...),
	}
}
