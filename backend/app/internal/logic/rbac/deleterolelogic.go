// Code scaffolded by goctl. Safe to edit.
// goctl 1.10.1

package rbac

import (
	"context"
	"database/sql"

	"app/internal/common"
	"app/internal/svc"
	"app/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type DeleteRoleLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewDeleteRoleLogic(ctx context.Context, svcCtx *svc.ServiceContext) *DeleteRoleLogic {
	return &DeleteRoleLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *DeleteRoleLogic) DeleteRole(req *types.DeleteRoleReq) (resp *types.DeleteRoleResp, err error) {
	if err := common.RequirePermission(l.ctx, l.svcCtx.CustomCtx.SQLModel, "role", "delete"); err != nil {
		return nil, err
	}

	// 通过内部 id 查找角色
	role, err := l.svcCtx.CustomCtx.SQLModel.RolesModel.FindOne(l.ctx, req.RoleId)
	if err != nil {
		l.Logger.Errorf("role not found: roleId=%d, err=%v", req.RoleId, err)
		return &types.DeleteRoleResp{
			BaseResp: types.BaseResp{Code: 404, Message: MsgRoleNotFound},
		}, nil
	}

	// 删除角色关联数据
	if err = l.deleteRolePermissions(role.Id); err != nil {
		l.Logger.Errorf("delete role_permissions for role %d failed: %v", req.RoleId, err)
		return &types.DeleteRoleResp{
			BaseResp: types.BaseResp{Code: 500, Message: MsgRolePermCleanFailed},
		}, err
	}
	if err = l.deleteUserRolesByRole(role.Id); err != nil {
		l.Logger.Errorf("delete user_roles for role %d failed: %v", req.RoleId, err)
		return &types.DeleteRoleResp{
			BaseResp: types.BaseResp{Code: 500, Message: MsgRoleUserClearFailed},
		}, err
	}

	// 软删除角色：通过 model Update 设置 deleted_at，确保 go-zero 缓存同步失效
	role.DeletedAt = sql.NullTime{Time: common.Now(), Valid: true}
	err = l.svcCtx.CustomCtx.SQLModel.RolesModel.Update(l.ctx, role)
	if err != nil {
		l.Logger.Errorf("soft delete role %d failed: %v", req.RoleId, err)
		return &types.DeleteRoleResp{
			BaseResp: types.BaseResp{Code: 500, Message: MsgRoleDeleteFailed},
		}, err
	}

	return &types.DeleteRoleResp{
		BaseResp: types.BaseResp{Code: 200, Message: MsgSuccess},
	}, nil
}

// deleteRolePermissions 删除指定角色的所有权限关联
func (l *DeleteRoleLogic) deleteRolePermissions(roleId int64) error {
	_, err := (*l.svcCtx.CustomCtx.SqlxConn).ExecCtx(l.ctx,
		"DELETE FROM role_permissions WHERE role_id = ?", roleId)
	return err
}

// deleteUserRolesByRole 删除指定角色的所有用户关联
func (l *DeleteRoleLogic) deleteUserRolesByRole(roleId int64) error {
	_, err := (*l.svcCtx.CustomCtx.SqlxConn).ExecCtx(l.ctx,
		"DELETE FROM user_roles WHERE role_id = ?", roleId)
	return err
}
