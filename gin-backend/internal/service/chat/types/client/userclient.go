package client

import (
	"gin-backend/internal/service/chat/types/message"
	"sync"
	"sync/atomic"
)

// UserChannel 用户级连接通道: 封装 WebSocket 连接的消息投递
//   - msgCh: 类型化消息投递队列 (Online 协程消费并经 client.Send 发送)
//
// 并发安全: mu 保护 msgCh 的写入与关闭, 避免 Push/Offline 竞态 (send-on-closed)
type UserChannel struct {
	mu      sync.Mutex
	subject string
	session string
	alive   atomic.Bool
	msgCh   chan message.Message // 类型化消息通道 (投递队列)
	done    chan struct{}        // Offline 后关闭，供 HTTP Handler 结束长连接
	client  Client               // 底层 WebSocket 连接
}

// NewUserChannel 创建用户通道
func NewUserChannel(subject, sessionID string, c Client) *UserChannel {
	return &UserChannel{
		subject: subject,
		session: sessionID,
		msgCh:   make(chan message.Message, 64),
		done:    make(chan struct{}),
		client:  c,
	}
}

// Online 启动异步投递协程: 消费 msgCh 并经 client.Send 发送到连接
// 通道关闭或发送失败时退出
func (uc *UserChannel) Online() {
	uc.alive.Store(true)
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
	close(uc.done)
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

// SessionID 返回该连接建立时绑定的 JWT 会话标识。
func (uc *UserChannel) SessionID() string { return uc.session }

// Alive 返回是否存活
func (uc *UserChannel) Alive() bool { return uc.alive.Load() }

// Done 在连接被 Offline 时关闭，供 WebSocket Handler 及时结束。
func (uc *UserChannel) Done() <-chan struct{} { return uc.done }
