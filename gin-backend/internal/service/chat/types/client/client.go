// Package client 定义聊天 WebSocket 连接抽象。
package client

import "gin-backend/internal/service/chat/types/message"

// Client 封装聊天 WebSocket 连接。
type Client interface {
	Close() error                 // 关闭连接 (幂等)
	Send(m message.Message) error // 非阻塞出站投递 (缓冲满丢弃)
}
