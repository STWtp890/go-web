// 用户连接注册表: subject → *UserChannel (WS/SSE 同表互通)
package bridge

import (
	"sync"

	"gin-backend/internal/service/chat/types/client"
)

// UserClientMap 用户连接注册表 (subject → *UserChannel), 并发安全
// WS 与 SSE 连接注册进同一张表, 投递按 subject 查表 → 传输无关, 天然互通
type UserClientMap struct {
	mu sync.RWMutex
	uc map[string]*client.UserChannel
}

// NewUserClientMap 创建用户连接注册表
func NewUserClientMap() *UserClientMap {
	return &UserClientMap{uc: make(map[string]*client.UserChannel)}
}

// Attach 注册连接 (单连接语义, 同 subject 先下线旧连接)
func (m *UserClientMap) Attach(subject string, uc *client.UserChannel) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.uc[subject]; ok {
		old.Offline()
	}
	m.uc[subject] = uc
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
