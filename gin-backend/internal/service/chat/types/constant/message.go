// 消息与群组类型常量
package constant

const (
	TypeText   = "text"   // 文本消息
	TypeSystem = "system" // 系统通知（上线/下线/入群）
	TypeAck    = "ack"    // 服务端收到确认
	TypeError  = "error"  // 错误消息
)

// 群组类型
const (
	GroupPrivate = "private" // 私聊
	GroupGroup   = "group"   // 群聊
)
