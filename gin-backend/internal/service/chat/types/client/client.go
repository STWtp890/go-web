// Package client 定义聊天连接抽象与 WebSocket 传输实现。
package client

import (
	"gin-backend/internal/service/chat/types/message"
)

// Client 封装聊天 WebSocket 连接。
type Client interface {
	Close() error                 // 关闭连接 (幂等)
	Send(m message.Message) error // 有界等待出站投递，连接关闭或背压超时时返回错误
}

// Connection 是可由聊天服务管理生命周期的双向连接。
// 具体传输协议负责实现读写泵与心跳，业务层只消费入站字节并发送类型化消息。
type Connection interface {
	Client
	Start(onStop func())
	Incoming() <-chan []byte
}