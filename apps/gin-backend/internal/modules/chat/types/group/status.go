// 群聊用户状态: 区分"在库"与"在群"两层权限模型 (应用级聊天室场景)
//
//	InSystem "在库": 系统注册用户 (users 表), 由 JWT 鉴权 (AuthRequired) 保证
//	             → 具备"进入群聊"资格: 可申请加入聊天室, 加入后转变为 InGroup
//	InGroup  "在群": 聊天室成员 (chat_group_members, 内存 GroupMap)
//	             → 具备"群聊内操作"资格: 发消息 / 查看成员 / 退出, 前提 InSystem
//
// 状态转换 (先写 DB 后入内存, 原子):
//
//	InSystem --JoinGroup--> InGroup
//	InGroup  --LeaveGroup--> InSystem
package group

// 群聊用户状态
const (
	// StatusInSystem 在库: 系统注册用户, 可申请加入聊天室 (转变为在群)
	StatusInSystem = "in_system"
	// StatusInGroup 在群: 聊天室成员, 可进行群聊内操作 (发消息/查看成员/退出)
	StatusInGroup = "in_group"
)
