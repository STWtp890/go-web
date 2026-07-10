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

type CreateRoleLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewCreateRoleLogic(ctx context.Context, svcCtx *svc.ServiceContext) *CreateRoleLogic {
	return &CreateRoleLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *CreateRoleLogic) CreateRole(req *types.CreateRoleReq) (resp *types.CreateRoleResp, err error) {
	if err := common.RequirePermission(l.ctx, l.svcCtx.CustomCtx.SQLModel, "role", "create"); err != nil {
		return nil, err
	}

	result, err := l.svcCtx.CustomCtx.SQLModel.RolesModel.Insert(l.ctx, &rbac.Roles{
		Name:        req.RoleName,
		Description: req.Description,
	})
	if err != nil {
		l.Logger.Errorf("insert role failed: %v", err)
		return &types.CreateRoleResp{
			BaseResp: types.BaseResp{Code: 500, Message: "create role failed"},
		}, err
	}

	roleId, err := result.LastInsertId()
	if err != nil {
		l.Logger.Errorf("get last insert id failed: %v", err)
		return &types.CreateRoleResp{
			BaseResp: types.BaseResp{Code: 500, Message: MsgRoleCreateFailed},
		}, err
	}

	// 关联权限
	if len(req.Permissions) > 0 {
		if err = l.syncRolePermissions(roleId, req.Permissions); err != nil {
			l.Logger.Errorf("sync role permissions failed: %v", err)
			return &types.CreateRoleResp{
				BaseResp: types.BaseResp{Code: 500, Message: MsgRolePermAssignFailed},
			}, err
		}
	}

	return &types.CreateRoleResp{
		BaseResp: types.BaseResp{Code: 200, Message: MsgSuccess},
		CreateRoleRespBody: types.CreateRoleRespBody{
			Role: types.Role{
				RoleId:      roleId,
				RoleName:    req.RoleName,
				Description: req.Description,
				Permissions: req.Permissions,
			},
		},
	}, nil
}

// syncRolePermissions 全量同步角色的权限关联（先清后插）
func (l *CreateRoleLogic) syncRolePermissions(roleId int64, permIds []int64) error {
	if err := l.deleteRolePermissions(roleId); err != nil {
		return err
	}
	for _, permId := range permIds {
		if _, err := l.svcCtx.CustomCtx.SQLModel.PermissionsModel.FindOne(l.ctx, permId); err != nil {
			l.Logger.Errorf("permission not found: id=%d", permId)
			return err
		}
		if err := l.insertRolePermission(roleId, permId); err != nil {
			return err
		}
	}
	return nil
}

// deleteRolePermissions 删除指定角色的所有权限关联
func (l *CreateRoleLogic) deleteRolePermissions(roleId int64) error {
	_, err := (*l.svcCtx.CustomCtx.SqlxConn).ExecCtx(l.ctx,
		"DELETE FROM role_permissions WHERE role_id = ?", roleId)
	return err
}

// insertRolePermission 插入单条角色-权限关联
func (l *CreateRoleLogic) insertRolePermission(roleId, permissionId int64) error {
	_, err := l.svcCtx.CustomCtx.SQLModel.RolePermissionsModel.Insert(l.ctx, &rbac.RolePermissions{
		RoleId:       roleId,
		PermissionId: permissionId,
	})
	return err
}
