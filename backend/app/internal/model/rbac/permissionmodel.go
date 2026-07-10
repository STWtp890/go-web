package rbac

import (
	"github.com/zeromicro/go-zero/core/stores/cache"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

var _ PermissionModel = (*customPermissionModel)(nil)

type (
	// PermissionModel is an interface to be customized, add more methods here,
	// and implement the added methods in customPermissionModel.
	PermissionModel interface {
		permissionModel
	}

	customPermissionModel struct {
		*defaultPermissionModel
	}
)

// NewPermissionModel returns a model for the database table.
func NewPermissionModel(conn sqlx.SqlConn, c cache.CacheConf, opts ...cache.Option) PermissionModel {
	return &customPermissionModel{
		defaultPermissionModel: newPermissionModel(conn, c, opts...),
	}
}
