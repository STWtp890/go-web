package rbac

import (
	"context"
	"fmt"

	"github.com/zeromicro/go-zero/core/stores/cache"
	"github.com/zeromicro/go-zero/core/stores/sqlc"
	"github.com/zeromicro/go-zero/core/stores/sqlx"
)

var _ UserRolesModel = (*customUserRolesModel)(nil)

type (
	// UserRolesModel is an interface to be customized, add more methods here,
	// and implement the added methods in customUserRolesModel.
	UserRolesModel interface {
		userRolesModel
		FindOneByUserId(ctx context.Context, userId string) (*UserRoles, error)
	}

	customUserRolesModel struct {
		*defaultUserRolesModel
	}
)

// NewUserRolesModel returns a model for the database table.
func NewUserRolesModel(conn sqlx.SqlConn, c cache.CacheConf, opts ...cache.Option) UserRolesModel {
	return &customUserRolesModel{
		defaultUserRolesModel: newUserRolesModel(conn, c, opts...),
	}
}

// FindOneByUserId 通过 user_id (UUID) 查询用户的第一条角色记录
func (m *customUserRolesModel) FindOneByUserId(ctx context.Context, userId string) (*UserRoles, error) {
	var resp UserRoles
	query := fmt.Sprintf("select %s from %s where `user_id` = ? limit 1", userRolesRows, m.table)
	err := m.QueryRowNoCacheCtx(ctx, &resp, query, userId)
	switch err {
	case nil:
		return &resp, nil
	case sqlc.ErrNotFound:
		return nil, ErrNotFound
	default:
		return nil, err
	}
}
