// Package client 定义聊天连接统一抽象: WebSocket 连接或 SSE 流
package client

import "gin-backend/internal/service/chat/types/message"

// Client 统一聊天连接封装: WebSocket 连接或 SSE 流
type Client interface {
	Close() error                 // 关闭连接 (幂等)
	Send(m message.Message) error // 非阻塞出站投递 (缓冲满丢弃)
}
