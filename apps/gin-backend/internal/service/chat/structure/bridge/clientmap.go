// 用户 WebSocket 连接注册表: subject → *UserChannel
package bridge

import (
	"sync"

	"gin-backend/internal/service/chat/types/client"
)

// UserClientMap 用户连接注册表 (subject → *UserChannel), 并发安全
type UserClientMap struct {
	mu sync.RWMutex
	uc map[string]*client.UserChannel
}

// NewUserClientMap 创建用户连接注册表
func NewUserClientMap() *UserClientMap {
	return &UserClientMap{uc: make(map[string]*client.UserChannel)}
}

// Attach 注册连接 (单连接语义, 同 subject 先下线旧连接)。关闭旧连接在锁外执行，
// 避免底层网络关闭阻塞注册表操作。
func (m *UserClientMap) Attach(subject string, uc *client.UserChannel) {
	m.mu.Lock()
	old := m.uc[subject]
	m.uc[subject] = uc
	m.mu.Unlock()
	if old != nil && old != uc {
		old.Offline()
	}
}

// Detach 移除连接 (引用比较, 防旧连接清理误删新连接)
func (m *UserClientMap) Detach(subject string, uc *client.UserChannel) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cur, ok := m.uc[subject]; ok && cur == uc {
		delete(m.uc, subject)
	}
}

// Get 获取连接; 不存在返回 nil
func (m *UserClientMap) Get(subject string) *client.UserChannel {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.uc[subject]
}

// RevokeSession 仅关闭 subject 当前连接中与 sid 匹配的会话。事件重复、旧事件
// 或连接已自行断开时都返回 0，保证消费端可安全重试。
func (m *UserClientMap) RevokeSession(subject, sessionID string) int {
	m.mu.Lock()
	uc := m.uc[subject]
	if uc == nil || uc.SessionID() != sessionID {
		m.mu.Unlock()
		return 0
	}
	delete(m.uc, subject)
	m.mu.Unlock()

	uc.Offline()
	return 1
}
