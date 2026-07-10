// Code scaffolded by goctl. Safe to edit.
// goctl 1.10.1

package rbac

import (
	"context"
	"fmt"
	"strings"

	"app/internal/common"
	"app/internal/model/rbac"
	"app/internal/svc"
	"app/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type ListRolesLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewListRolesLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ListRolesLogic {
	return &ListRolesLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *ListRolesLogic) ListRoles(req *types.ListRolesReq) (resp *types.ListRoleResp, err error) {
	if err := common.RequirePermission(l.ctx, l.svcCtx.CustomCtx.SQLModel, "role", "list"); err != nil {
		return nil, err
	}

	pageInfo, dbRoles, err := common.PaginateQuery[rbac.Roles](
		l.ctx, *l.svcCtx.CustomCtx.SqlxConn,
		"SELECT COUNT(*) FROM roles WHERE deleted_at IS NULL", nil,
		"SELECT id, name, description, deleted_at FROM roles WHERE deleted_at IS NULL ORDER BY id", nil,
		req.PageNumber, req.PageSize,
	)
	if err != nil {
		l.Logger.Errorf("list roles failed: %v", err)
		return &types.ListRoleResp{
			BaseResp: types.BaseResp{Code: 500, Message: MsgQueryFailed},
		}, err
	}

	if len(dbRoles) == 0 {
		return &types.ListRoleResp{
			BaseResp: types.BaseResp{Code: 200, Message: MsgSuccess},
			PageInfo: *pageInfo,
			ListRoleRespBody: types.ListRoleRespBody{
				RoleList: types.RoleList{
					RoleList: []types.Role{},
				},
			},
		}, nil
	}

	roleIds := make([]int64, len(dbRoles))
	for i, r := range dbRoles {
		roleIds[i] = r.Id
	}

	permMap, err := l.batchQueryRolePermissions(roleIds)
	if err != nil {
		l.Logger.Errorf("batch query role permissions failed: %v", err)
		return &types.ListRoleResp{
			BaseResp: types.BaseResp{Code: 500, Message: MsgQueryFailed},
		}, err
	}

	roles := make([]types.Role, 0, len(dbRoles))
	for _, r := range dbRoles {
		roles = append(roles, types.Role{
			RoleId:      r.Id,
			RoleName:    r.Name,
			Description: r.Description,
			Permissions: permMap[r.Id],
		})
	}

	return &types.ListRoleResp{
		BaseResp: types.BaseResp{Code: 200, Message: MsgSuccess},
		PageInfo: *pageInfo,
		ListRoleRespBody: types.ListRoleRespBody{
			RoleList: types.RoleList{RoleList: roles},
		},
	}, nil
}

// batchQueryRolePermissions 批量查询多个角色的权限，返回 roleId → []int64 (权限内部 ID 列表)
func (l *ListRolesLogic) batchQueryRolePermissions(roleIds []int64) (map[int64][]int64, error) {
	if len(roleIds) == 0 {
		return map[int64][]int64{}, nil
	}

	placeholders := make([]string, len(roleIds))
	args := make([]any, len(roleIds))
	for i, id := range roleIds {
		placeholders[i] = "?"
		args[i] = id
	}

	type permRow struct {
		RoleId       int64 `db:"role_id"`
		PermissionId int64 `db:"permission_id"`
	}
	var rows []permRow
	query := fmt.Sprintf(
		`SELECT rp.role_id, rp.permission_id FROM role_permissions rp
		 WHERE rp.role_id IN (%s)`, strings.Join(placeholders, ","))
	if err := (*l.svcCtx.CustomCtx.SqlxConn).QueryRowsCtx(l.ctx, &rows, query, args...); err != nil {
		return nil, err
	}

	result := make(map[int64][]int64, len(roleIds))
	for _, row := range rows {
		result[row.RoleId] = append(result[row.RoleId], row.PermissionId)
	}
	// 确保没有权限的角色也有空 slice（而非 nil）
	for _, id := range roleIds {
		if _, ok := result[id]; !ok {
			result[id] = []int64{}
		}
	}
	return result, nil
}
