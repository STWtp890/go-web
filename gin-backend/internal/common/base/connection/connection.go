// Package connection 连接管理框架: 采用注册模式 (Register / Unregister / Get 三个原语),
// 各连接类型的"创建与初始化"位于其子包 (redis / mysql / websocket / sse 等),
// 连接的生命周期由对应业务决定并维护 (例如 chat.Hub 自行管理连接注册与注销)。
package connection

// 预定义服务名: 业务注册连接时使用, 获取时须按相同名称
const (
	// ServiceDefault 默认共享连接
	ServiceDefault = "default"
	// ServiceAuth 认证业务连接 (PostgreSQL / Redis)
	ServiceAuth = "auth"
	// ServiceMarkdown 文档业务连接 (PostgreSQL)
	ServiceMarkdown = "markdown"
	// ServiceCache 实体缓存专用连接 (Redis, 与 token 状态隔离)
	ServiceCache = "cache"
)
