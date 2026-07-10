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

type CreatePermissionLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewCreatePermissionLogic(ctx context.Context, svcCtx *svc.ServiceContext) *CreatePermissionLogic {
	return &CreatePermissionLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *CreatePermissionLogic) CreatePermission(req *types.CreatePermissionReq) (resp *types.CreatePermissionResp, err error) {
	if err := common.RequirePermission(l.ctx, l.svcCtx.CustomCtx.SQLModel, "permission", "create"); err != nil {
		return nil, err
	}

	p := req.Permission

	result, err := l.svcCtx.CustomCtx.SQLModel.PermissionsModel.Insert(l.ctx, &rbac.Permissions{
		Resource: p.Resource,
		Action:   p.Action,
		Label:    p.Label,
	})
	if err != nil {
		l.Logger.Errorf("insert permission failed: %v", err)
		return &types.CreatePermissionResp{
			BaseResp: types.BaseResp{Code: 500, Message: MsgPermCreateFailed},
		}, err
	}

	permissionId, err := result.LastInsertId()
	if err != nil {
		l.Logger.Errorf("get last insert id failed: %v", err)
		return &types.CreatePermissionResp{
			BaseResp: types.BaseResp{Code: 500, Message: MsgPermCreateFailed},
		}, err
	}
	p.PermissionId = permissionId

	return &types.CreatePermissionResp{
		BaseResp:                  types.BaseResp{Code: 200, Message: MsgSuccess},
		CreatePermissionRespBody:  types.CreatePermissionRespBody{Permission: p},
	}, nil
}
