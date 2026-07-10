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

type ListPermissionsLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewListPermissionsLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ListPermissionsLogic {
	return &ListPermissionsLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *ListPermissionsLogic) ListPermissions(req *types.ListPermissionsReq) (resp *types.ListPermissionResp, err error) {
	if err := common.RequirePermission(l.ctx, l.svcCtx.CustomCtx.SQLModel, "permission", "list"); err != nil {
		return nil, err
	}

	pageInfo, dbList, err := common.PaginateQuery[rbac.Permissions](
		l.ctx, *l.svcCtx.CustomCtx.SqlxConn,
		"SELECT COUNT(*) FROM permissions", nil,
		"SELECT id, resource, action, label FROM permissions ORDER BY id", nil,
		req.PageNumber, req.PageSize,
	)
	if err != nil {
		l.Logger.Errorf("list permissions failed: %v", err)
		return &types.ListPermissionResp{
			BaseResp: types.BaseResp{Code: 500, Message: MsgQueryFailed},
		}, err
	}

	permissions := make([]types.Permission, 0, len(dbList))
	for _, p := range dbList {
		permissions = append(permissions, types.Permission{
			PermissionId: p.Id,
			Resource:     p.Resource,
			Action:       p.Action,
			Label:        p.Label,
		})
	}

	return &types.ListPermissionResp{
		BaseResp: types.BaseResp{Code: 200, Message: MsgSuccess},
		PageInfo: *pageInfo,
		ListPermissionRespBody: types.ListPermissionRespBody{
			PermissionList: types.PermissionList{PermissionList: permissions},
		},
	}, nil
}
