package client

import (
	"gin-backend/internal/service/chat/types/message"
	"sync"
	"sync/atomic"
)

// UserChannel 用户级连接通道: 统一封装 SSE / WebSocket 连接的消息投递
//   - msgCh: 类型化消息投递队列 (Online 协程消费并经 client.Send 发送)
//
// 并发安全: mu 保护 msgCh 的写入与关闭, 避免 Push/Offline 竞态 (send-on-closed)
type UserChannel struct {
	mu      sync.Mutex
	subject string
	alive   atomic.Bool
	msgCh   chan message.Message // 类型化消息通道 (投递队列)
	client  Client               // 底层连接 (SSE / WebSocket)
}

// NewUserChannel 创建用户通道
func NewUserChannel(subject string, c Client) *UserChannel {
	return &UserChannel{
		subject: subject,
		msgCh:   make(chan message.Message, 64),
		client:  c,
	}
}

// Online 启动异步投递协程: 消费 msgCh 并经 client.Send 发送到连接
// 通道关闭或发送失败时退出
func (uc *UserChannel) Online() {
	go func() {
		for m := range uc.msgCh {
			if !uc.alive.Load() {
				return
			}
			if err := uc.client.Send(m); err != nil {
				uc.alive.Store(false)
				return
			}
		}
	}()
}

// Offline 下线: 标记非存活, 关闭投递通道并关闭底层连接 (幂等)
func (uc *UserChannel) Offline() {
	uc.mu.Lock()
	defer uc.mu.Unlock()
	if !uc.alive.Swap(false) {
		return
	}
	close(uc.msgCh)
	_ = uc.client.Close()
}

// Push 投递一条消息到用户通道 (非阻塞, 缓冲满或已下线返回 false)
func (uc *UserChannel) Push(m message.Message) bool {
	uc.mu.Lock()
	defer uc.mu.Unlock()
	if !uc.alive.Load() {
		return false
	}
	select {
	case uc.msgCh <- m:
		return true
	default:
		return false
	}
}

// Subject 返回用户标识
func (uc *UserChannel) Subject() string { return uc.subject }

// Alive 返回是否存活
func (uc *UserChannel) Alive() bool { return uc.alive.Load() }
