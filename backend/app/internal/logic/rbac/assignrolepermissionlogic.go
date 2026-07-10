// Code scaffolded by goctl. Safe to edit.
// goctl 1.10.1

package rbac

import (
	"context"

	"app/internal/common"
	"app/internal/model/rbac"
	"app/internal/svc"
	"app/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type AssignRolePermissionLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewAssignRolePermissionLogic(ctx context.Context, svcCtx *svc.ServiceContext) *AssignRolePermissionLogic {
	return &AssignRolePermissionLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *AssignRolePermissionLogic) AssignRolePermission(req *types.AssignRolePermissionReq) (resp *types.AssignRolePermissionResp, err error) {
	if err := common.RequirePermission(l.ctx, l.svcCtx.CustomCtx.SQLModel, "permission", "assign"); err != nil {
		return nil, err
	}

	// 通过内部 id 查找角色
	role, err := l.svcCtx.CustomCtx.SQLModel.RolesModel.FindOne(l.ctx, req.RoleId)
	if err != nil {
		l.Logger.Errorf("role not found: roleId=%d", req.RoleId)
		return &types.AssignRolePermissionResp{
			BaseResp: types.BaseResp{Code: 404, Message: "role not found"},
		}, nil
	}
	if role.DeletedAt.Valid {
		l.Logger.Errorf("role has been deleted: roleId=%d", req.RoleId)
		return &types.AssignRolePermissionResp{
			BaseResp: types.BaseResp{Code: 404, Message: "role not found"},
		}, nil
	}

	// 删除旧关联 (使用内部 id)
	if err = l.deleteRolePermissions(role.Id); err != nil {
		l.Logger.Errorf("delete old role_permissions failed: %v", err)
		return &types.AssignRolePermissionResp{
			BaseResp: types.BaseResp{Code: 500, Message: MsgRolePermClearFailed},
		}, err
	}

	// 插入新关联 (Permissions 为权限内部 ID 列表)，同时收集权限详情用于响应
	perms := make([]types.Permission, 0, len(req.Permissions))
	for _, permId := range req.Permissions {
		p, err := l.svcCtx.CustomCtx.SQLModel.PermissionsModel.FindOne(l.ctx, permId)
		if err != nil {
			l.Logger.Errorf("permission not found: id=%d", permId)
			return &types.AssignRolePermissionResp{
				BaseResp: types.BaseResp{Code: 400, Message: "permission not found"},
			}, nil
		}

		if err = l.insertRolePermission(role.Id, p.Id); err != nil {
			l.Logger.Errorf("insert role_permission failed: %v", err)
			return &types.AssignRolePermissionResp{
				BaseResp: types.BaseResp{Code: 500, Message: MsgPermAssignFailed},
			}, err
		}

		perms = append(perms, types.Permission{
			PermissionId: p.Id,
			Resource:     p.Resource,
			Action:       p.Action,
			Label:        p.Label,
		})
	}

	permsId := make([]int64, 0, len(perms))
	for _, p := range perms {
		permsId = append(permsId, p.PermissionId)
	}

	return &types.AssignRolePermissionResp{
		BaseResp: types.BaseResp{Code: 200, Message: MsgSuccess},
		AssignRolePermissionRespBody: types.AssignRolePermissionRespBody{
			Role: types.Role{
				RoleId:      role.Id,
				RoleName:    role.Name,
				Description: role.Description,
				Permissions: permsId,
			},
		},
	}, nil
}

// deleteRolePermissions 删除指定角色的所有权限关联
func (l *AssignRolePermissionLogic) deleteRolePermissions(roleId int64) error {
	_, err := (*l.svcCtx.CustomCtx.SqlxConn).ExecCtx(l.ctx,
		"DELETE FROM role_permissions WHERE role_id = ?", roleId)
	return err
}

// insertRolePermission 插入单条角色-权限关联
func (l *AssignRolePermissionLogic) insertRolePermission(roleId, permissionId int64) error {
	_, err := l.svcCtx.CustomCtx.SQLModel.RolePermissionsModel.Insert(l.ctx, &rbac.RolePermissions{
		RoleId:       roleId,
		PermissionId: permissionId,
	})
	return err
}
