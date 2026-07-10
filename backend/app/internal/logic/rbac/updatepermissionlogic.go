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

type UpdatePermissionLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewUpdatePermissionLogic(ctx context.Context, svcCtx *svc.ServiceContext) *UpdatePermissionLogic {
	return &UpdatePermissionLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *UpdatePermissionLogic) UpdatePermission(req *types.UpdatePermissionReq) (resp *types.UpdatePermissionResp, err error) {
	if err := common.RequirePermission(l.ctx, l.svcCtx.CustomCtx.SQLModel, "permission", "update"); err != nil {
		return nil, err
	}

	p := req.Permission

	// 通过内部 id 查找权限
	existing, err := l.svcCtx.CustomCtx.SQLModel.PermissionsModel.FindOne(l.ctx, req.PermissionId)
	if err != nil {
		l.Logger.Errorf("permission not found: id=%d, err=%v", req.PermissionId, err)
		return &types.UpdatePermissionResp{
			BaseResp: types.BaseResp{Code: 404, Message: MsgPermNotFound},
		}, nil
	}

	err = l.svcCtx.CustomCtx.SQLModel.PermissionsModel.Update(l.ctx, &rbac.Permissions{
		Id:       existing.Id,
		Resource: p.Resource,
		Action:   p.Action,
		Label:    p.Label,
	})
	if err != nil {
		l.Logger.Errorf("update permission failed: %v", err)
		return &types.UpdatePermissionResp{
			BaseResp: types.BaseResp{Code: 500, Message: MsgPermUpdateFailed},
		}, err
	}

	p.PermissionId = existing.Id
	return &types.UpdatePermissionResp{
		BaseResp:                  types.BaseResp{Code: 200, Message: MsgSuccess},
		UpdatePermissionRespBody:  types.UpdatePermissionRespBody{Permission: p},
	}, nil
}
