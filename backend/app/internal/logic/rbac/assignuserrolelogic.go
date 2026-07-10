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

type AssignUserRoleLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewAssignUserRoleLogic(ctx context.Context, svcCtx *svc.ServiceContext) *AssignUserRoleLogic {
	return &AssignUserRoleLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *AssignUserRoleLogic) AssignUserRole(req *types.AssignUserRoleReq) (resp *types.AssignUserRoleResp, err error) {
	if err := common.RequirePermission(l.ctx, l.svcCtx.CustomCtx.SQLModel, "role", "assign"); err != nil {
		return nil, err
	}

	// 通过 userId 查找用户
	user, err := l.svcCtx.CustomCtx.SQLModel.UsersModel.FindOneByUserId(l.ctx, req.UserId)
	if err != nil {
		l.Logger.Errorf("user not found: userId=%s", req.UserId)
		return &types.AssignUserRoleResp{
			BaseResp: types.BaseResp{Code: 404, Message: MsgUserNotFound},
		}, nil
	}

	if err = l.deleteUserRolesByUser(user.UserId); err != nil {
		l.Logger.Errorf("delete old user_roles for user %s failed: %v", req.UserId, err)
		return &types.AssignUserRoleResp{
			BaseResp: types.BaseResp{Code: 500, Message: MsgUserRoleClearFailed},
		}, err
	}

	for _, roleId := range req.Roles {
		role, err := l.svcCtx.CustomCtx.SQLModel.RolesModel.FindOne(l.ctx, roleId)
		if err != nil {
			l.Logger.Errorf("role not found: roleId=%d", roleId)
			return &types.AssignUserRoleResp{
				BaseResp: types.BaseResp{Code: 400, Message: "role not found"},
			}, nil
		}
		if role.DeletedAt.Valid {
			l.Logger.Errorf("role has been deleted: roleId=%d", roleId)
			return &types.AssignUserRoleResp{
				BaseResp: types.BaseResp{Code: 400, Message: "role not found"},
			}, nil
		}

		_, err = l.svcCtx.CustomCtx.SQLModel.UserRolesModel.Insert(l.ctx, &rbac.UserRoles{
			UserId: user.UserId,
			RoleId: role.Id,
		})
		if err != nil {
			l.Logger.Errorf("insert user_role failed: userId=%s, roleId=%d, err=%v", user.UserId, role.Id, err)
			return &types.AssignUserRoleResp{
				BaseResp: types.BaseResp{Code: 500, Message: MsgUserRoleAssignFailed},
			}, err
		}
	}

	return &types.AssignUserRoleResp{
		BaseResp: types.BaseResp{Code: 200, Message: MsgSuccess},
	}, nil
}

// deleteUserRolesByUser 删除指定用户的所有角色关联
func (l *AssignUserRoleLogic) deleteUserRolesByUser(userId string) error {
	_, err := (*l.svcCtx.CustomCtx.SqlxConn).ExecCtx(l.ctx,
		"DELETE FROM user_roles WHERE user_id = ?", userId)
	return err
}
