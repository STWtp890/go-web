package rbac

// 响应消息常量
const (
	MsgSuccess              = "success"
	MsgQueryFailed          = "query failed"
	MsgRoleNotFound         = "role not found"
	MsgRoleCreateFailed     = "create role failed"
	MsgRoleUpdateFailed     = "update role failed"
	MsgRoleDeleteFailed     = "delete role failed"
	MsgRolePermAssignFailed = "failed to assign permissions"
	MsgRolePermClearFailed  = "failed to clear old permissions"
	MsgRoleUserClearFailed  = "failed to clean up user roles"
	MsgRolePermCleanFailed  = "failed to clean up role permissions"
	MsgPermNotFound         = "permission not found"
	MsgPermCreateFailed     = "create permission failed"
	MsgPermUpdateFailed     = "update permission failed"
	MsgPermDeleteFailed     = "delete permission failed"
	MsgPermAssignFailed     = "failed to assign permission"
	MsgPermRelDeleteFailed  = "delete related role_permissions failed"
	MsgPermUpdatePermFailed = "update permissions failed"
	MsgUserNotFound         = "user not found"
	MsgUserRoleClearFailed  = "failed to clear old roles"
	MsgUserRoleAssignFailed = "failed to assign role"
)
