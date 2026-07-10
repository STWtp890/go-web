package common

import (
	"context"
	"errors"

	"app/internal/svc"

	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/core/stores/sqlc"
)

// SuperAdminRoleId 超级管理员角色的内部主键 (roles.id)。
// superadmin 拥有系统所有权限，RBAC 检查时隐式放行。
const SuperAdminRoleId int64 = 1

// RequirePermission 校验用户是否拥有指定权限（resource:action）。
// 通过 goctl model 层完成查询，享受缓存加速。
// superadmin (role_id=1) 隐式放行，跳过所有检查。
// 权限不足时返回 *BizError，调用方应 return nil, err 向上传递。
func RequirePermission(ctx context.Context, m *svc.SQLModel, resource, action string) error {
	userId, err := checkUser(ctx, m)
	if err != nil {
		logx.WithContext(ctx).Errorf("rbac: checkUser failed: %v", err)
		return err
	}

	roleId, err := checkUserRole(ctx, m, userId)
	if err != nil {
		logx.WithContext(ctx).Errorf("rbac: checkUserRole failed (user=%s): %v", userId, err)
		return err
	}

	if roleId == SuperAdminRoleId {
		return nil
	}

	if err := checkRolePermission(ctx, m, roleId, resource, action); err != nil {
		logx.WithContext(ctx).Errorf("rbac: denied role=%d %s:%s — %v", roleId, resource, action, err)
		return err
	}

	return nil
}

// checkUser 检查用户是否存在、未被删除，并返回 userId。
func checkUser(ctx context.Context, m *svc.SQLModel) (string, error) {
	userId, err := GetUserIdFromCtx(ctx)
	if err != nil {
		return "", NewBizError(401, "unauthorized: invalid token")
	}

	user, err := m.UsersModel.FindOneByUserId(ctx, userId)
	if err != nil {
		return "", NewBizError(401, "unauthorized: user not found")
	}

	if user.DeletedAt.Valid {
		return "", NewBizError(401, "unauthorized: user deleted")
	}

	return user.UserId, nil
}

// checkUserRole 检查用户角色记录是否存在，并与 token 中的 roleId 交叉校验。
func checkUserRole(ctx context.Context, m *svc.SQLModel, userId string) (int64, error) {
	userRole, err := m.UserRolesModel.FindOneByUserId(ctx, userId)
	if err != nil {
		return -1, NewBizError(403, "forbidden: no role assigned")
	}

	roleId, err := GetRoleIdFromCtx(ctx)
	if err != nil {
		return -1, NewBizError(401, "unauthorized: invalid token")
	}

	if userRole.RoleId != roleId {
		return -1, NewBizError(403, "forbidden: role mismatch")
	}

	return userRole.RoleId, nil
}

// checkRolePermission 通过 model 层检查角色是否拥有指定权限。
func checkRolePermission(ctx context.Context, m *svc.SQLModel, roleId int64, resource, action string) error {
	perm, err := m.PermissionsModel.FindOneByResourceAction(ctx, resource, action)
	if err != nil {
		if errors.Is(err, sqlc.ErrNotFound) {
			return NewBizError(403, "forbidden: permission not defined")
		}
		return err
	}

	_, err = m.RolePermissionsModel.FindOneByRoleIdPermissionId(ctx, roleId, perm.Id)
	if err != nil {
		if errors.Is(err, sqlc.ErrNotFound) {
			return NewBizError(403, "forbidden: role lacks permission")
		}
		return err
	}

	return nil
}
