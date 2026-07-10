// Code scaffolded by goctl. Safe to edit.
// goctl 1.10.1

package rbac

import (
	"context"

	"app/internal/common"
	"app/internal/svc"
	"app/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type DeletePermissionLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewDeletePermissionLogic(ctx context.Context, svcCtx *svc.ServiceContext) *DeletePermissionLogic {
	return &DeletePermissionLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *DeletePermissionLogic) DeletePermission(req *types.DeletePermissionReq) (resp *types.DeletePermissionResp, err error) {
	if err := common.RequirePermission(l.ctx, l.svcCtx.CustomCtx.SQLModel, "permission", "delete"); err != nil {
		return nil, err
	}

	// 通过内部 id 查找权限
	perm, err := l.svcCtx.CustomCtx.SQLModel.PermissionsModel.FindOne(l.ctx, req.PermissionId)
	if err != nil {
		l.Logger.Errorf("permission not found: id=%d, err=%v", req.PermissionId, err)
		return &types.DeletePermissionResp{
			BaseResp: types.BaseResp{Code: 404, Message: MsgPermNotFound},
		}, nil
	}

	if err = l.deleteRolePermissionsByPermission(perm.Id); err != nil {
		l.Logger.Errorf("delete role_permissions for permission %d failed: %v", req.PermissionId, err)
		return &types.DeletePermissionResp{
			BaseResp: types.BaseResp{Code: 500, Message: MsgPermRelDeleteFailed},
		}, err
	}

	err = l.svcCtx.CustomCtx.SQLModel.PermissionsModel.Delete(l.ctx, perm.Id)
	if err != nil {
		l.Logger.Errorf("delete permission %d failed: %v", req.PermissionId, err)
		return &types.DeletePermissionResp{
			BaseResp: types.BaseResp{Code: 500, Message: MsgPermDeleteFailed},
		}, err
	}

	return &types.DeletePermissionResp{
		BaseResp: types.BaseResp{Code: 200, Message: MsgSuccess},
	}, nil
}

// deleteRolePermissionsByPermission 删除指定权限的所有角色关联
func (l *DeletePermissionLogic) deleteRolePermissionsByPermission(permissionId int64) error {
	_, err := (*l.svcCtx.CustomCtx.SqlxConn).ExecCtx(l.ctx,
		"DELETE FROM role_permissions WHERE permission_id = ?", permissionId)
	return err
}
