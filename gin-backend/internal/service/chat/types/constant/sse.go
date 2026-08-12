// SSE 传输层常量
package constant

import "time"

const (
	EventHeartbeat     = "heartbeat"      // SSE 心跳事件名
	EventMessage       = "message"        // SSE 消息事件名 (兜底, 通常使用消息类型作为 event 名)
	HeartbeatFrame     = ": ping"         // SSE 心跳注释帧 (注释行不触发客户端事件, 仅保活)
	HeartbeatInterval  = 15 * time.Second // SSE 心跳间隔
	OutboundBufferSize = 64               // SSE 出站事件缓冲大小
)
