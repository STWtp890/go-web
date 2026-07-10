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

type UpdateRoleLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewUpdateRoleLogic(ctx context.Context, svcCtx *svc.ServiceContext) *UpdateRoleLogic {
	return &UpdateRoleLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *UpdateRoleLogic) UpdateRole(req *types.UpdateRoleReq) (resp *types.UpdateRoleResp, err error) {
	if err := common.RequirePermission(l.ctx, l.svcCtx.CustomCtx.SQLModel, "role", "update"); err != nil {
		return nil, err
	}

	r := req.Role

	existingRole, err := l.svcCtx.CustomCtx.SQLModel.RolesModel.FindOne(l.ctx, req.RoleId)
	if err != nil {
		l.Logger.Errorf("role not found: roleId=%d, err=%v", req.RoleId, err)
		return &types.UpdateRoleResp{
			BaseResp: types.BaseResp{Code: 404, Message: MsgRoleNotFound},
		}, nil
	}

	err = l.svcCtx.CustomCtx.SQLModel.RolesModel.Update(l.ctx, &rbac.Roles{
		Id:          existingRole.Id,
		Name:        r.RoleName,
		Description: r.Description,
		DeletedAt:   existingRole.DeletedAt,
	})
	if err != nil {
		l.Logger.Errorf("update role failed: %v", err)
		return &types.UpdateRoleResp{
			BaseResp: types.BaseResp{Code: 500, Message: MsgRoleUpdateFailed},
		}, err
	}

	// 同步权限关联
	if len(r.Permissions) > 0 {
		if err = l.deleteRolePermissions(existingRole.Id); err != nil {
			l.Logger.Errorf("delete old role_permissions failed: %v", err)
			return &types.UpdateRoleResp{
				BaseResp: types.BaseResp{Code: 500, Message: MsgPermUpdatePermFailed},
			}, err
		}

		for _, permId := range r.Permissions {
			if _, err := l.svcCtx.CustomCtx.SQLModel.PermissionsModel.FindOne(l.ctx, permId); err != nil {
				l.Logger.Errorf("permission not found: id=%d", permId)
				return &types.UpdateRoleResp{
					BaseResp: types.BaseResp{Code: 400, Message: "permission not found"},
				}, nil
			}

			if err = l.insertRolePermission(existingRole.Id, permId); err != nil {
				l.Logger.Errorf("insert role_permission failed: %v", err)
				return &types.UpdateRoleResp{
					BaseResp: types.BaseResp{Code: 500, Message: MsgPermUpdatePermFailed},
				}, err
			}
		}
	}

	return &types.UpdateRoleResp{
		BaseResp: types.BaseResp{Code: 200, Message: MsgSuccess},
		UpdateRoleRespBody: types.UpdateRoleRespBody{
			Role: types.Role{
				RoleId:      existingRole.Id,
				RoleName:    r.RoleName,
				Description: r.Description,
				Permissions: r.Permissions,
			},
		},
	}, nil
}

// deleteRolePermissions 删除指定角色的所有权限关联
func (l *UpdateRoleLogic) deleteRolePermissions(roleId int64) error {
	_, err := (*l.svcCtx.CustomCtx.SqlxConn).ExecCtx(l.ctx,
		"DELETE FROM role_permissions WHERE role_id = ?", roleId)
	return err
}

// insertRolePermission 插入单条角色-权限关联
func (l *UpdateRoleLogic) insertRolePermission(roleId, permissionId int64) error {
	_, err := l.svcCtx.CustomCtx.SQLModel.RolePermissionsModel.Insert(l.ctx, &rbac.RolePermissions{
		RoleId:       roleId,
		PermissionId: permissionId,
	})
	return err
}
