// 群业务域: 内存群状态 (成员集合 + 最近消息窗口) 与路由映射
package group

import (
	"sync"
	"sync/atomic"

	"gin-backend/internal/service/chat/types/message"
)

// windowSize 群聊最近消息窗口容量 (成员上线/加入时补发最近消息)
const windowSize = 100

// GroupState 群聊状态: 成员集合 + 最近消息窗口 (泛型滑动窗口)
// 并发安全: mu 保护成员集合与加载标记; 窗口经 SafeQueue 自带锁
// loaded: 是否已从持久层恢复 (进程重启后首次访问时懒加载, DB 为成员真相源)
type GroupState struct {
	mu      sync.Mutex
	loaded  atomic.Bool // 是否已从持久层加载成员
	members map[string]struct{}
	window  *SafeQueue[message.Message]
}

// newGroupState 创建群聊状态
func newGroupState() *GroupState {
	return &GroupState{
		members: make(map[string]struct{}),
		window:  NewSafeQueue[message.Message](windowSize),
	}
}

// Loaded 返回是否已从持久层加载成员
func (s *GroupState) Loaded() bool { return s.loaded.Load() }

// MarkLoaded 标记已加载 (持久层不可用时仍标记, 避免每次投递都尝试加载)
func (s *GroupState) MarkLoaded() { s.loaded.Store(true) }

// ReplaceMembers 以持久层成员集合重建 (懒加载时调用, 幂等)
func (s *GroupState) ReplaceMembers(members []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.members = make(map[string]struct{}, len(members))
	for _, m := range members {
		s.members[m] = struct{}{}
	}
	s.loaded.Store(true)
}

// Join 加入群；可靠投递补发由 pending delivery 负责，不返回历史窗口。
func (s *GroupState) Join(subject string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.members[subject] = struct{}{}
}

// Leave 离开群
func (s *GroupState) Leave(subject string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.members, subject)
}

// Contains 判断成员是否存在 (内存查询, 群消息发送/查看前鉴权)
func (s *GroupState) Contains(subject string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.members[subject]
	return ok
}

// Members 返回成员列表副本
func (s *GroupState) Members() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.members))
	for m := range s.members {
		out = append(out, m)
	}
	return out
}

// Push 群消息入窗口 (滑动, 超容量挤最旧)
func (s *GroupState) Push(m message.Message) {
	s.window.ForcePushEnqueue(m)
}

// WindowEmpty 返回窗口是否为空 (懒加载恢复群历史时判断)
func (s *GroupState) WindowEmpty() bool {
	return s.window.Len() == 0
}

// ReplaceWindow 以持久层群历史重建窗口 (进程重启后懒加载恢复, 幂等)
func (s *GroupState) ReplaceWindow(msgs []message.Message) {
	s.window.Replace(msgs)
}

// GroupMap 群聊路由映射 (groupID → GroupState), 并发安全
type GroupMap struct {
	mu     sync.Mutex
	groups map[string]*GroupState
}

// NewGroupMap 创建群聊映射
func NewGroupMap() *GroupMap {
	return &GroupMap{groups: make(map[string]*GroupState)}
}

// Router 获取或创建群聊状态
func (g *GroupMap) Router(groupID string) *GroupState {
	g.mu.Lock()
	defer g.mu.Unlock()
	if s, ok := g.groups[groupID]; ok {
		return s
	}
	s := newGroupState()
	g.groups[groupID] = s
	return s
}

// Get 获取群状态; 不存在返回 nil (只读, 不创建)
func (g *GroupMap) Get(groupID string) *GroupState {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.groups[groupID]
}

// Remove 移除群聊状态
func (g *GroupMap) Remove(groupID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.groups, groupID)
}
