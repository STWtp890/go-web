// 群业务域: 群管理 (落库 + 同步内存 + 懒加载)
package group

import (
	"context"
	"errors"
	"log/slog"

	"gin-backend/internal/service/chat/store"
	"gin-backend/internal/service/chat/types/message"

	"github.com/google/uuid"
)

// 群管理错误
var (
	// ErrGroupServiceUnavailable 群持久化未接入 (DB 不可用), 群管理不可用
	ErrGroupServiceUnavailable = errors.New("group: 群持久化未接入, 群管理不可用")
	// ErrGroupNotFound 目标群不存在
	ErrGroupNotFound = errors.New("group: 群不存在")
)

// Manager 群管理器: 经 groupsStore 落库 (DB 为成员关系真相源) + 同步内存 GroupMap (投递加速)
// bridge 持 Manager 完成群投递 (Map) 与群管理 (Create/Join/Leave 等)
type Manager struct {
	groups      *GroupMap
	groupsStore store.GroupStore
	messages    store.MessageStore
}

// NewManager 创建群管理器
// :Param
// - `groupsStore` 群组持久化 (可为 nil, 降级纯内存)
// - `messages` 消息持久化 (懒加载群历史窗口用, 可为 nil)
func NewManager(groupsStore store.GroupStore, messages store.MessageStore) *Manager {
	return &Manager{
		groups:      NewGroupMap(),
		groupsStore: groupsStore,
		messages:    messages,
	}
}

// Groups 返回内存群状态映射 (供桥投递使用)
func (m *Manager) Groups() *GroupMap { return m.groups }

// CreateGroup 创建群: 生成群 ID (UUID), 落库 (owner 自动成为首个成员) 并初始化内存状态
// :Return
// - `string` 新群 ID
// - `error` 落库失败等
func (m *Manager) CreateGroup(ctx context.Context, name, ownerID string) (string, error) {
	if m.groupsStore == nil {
		return "", ErrGroupServiceUnavailable
	}
	groupID := uuid.NewString()
	if err := m.groupsStore.CreateGroup(ctx, groupID, name, ownerID); err != nil {
		return "", err
	}
	// 同步内存: owner 入群 (窗口为空, 无补发)
	m.groups.Router(groupID).Join(ownerID)
	return groupID, nil
}

// JoinGroup 加入群: 校验群存在 → 落库 + 同步内存; 返回窗口消息供接入方补发
// :Return
// - `[]message.Message` 群最近消息窗口 (调用方可补发给该成员)
// - `error` ErrGroupNotFound / ErrGroupServiceUnavailable / 落库失败
func (m *Manager) JoinGroup(ctx context.Context, groupID, memberID string) ([]message.Message, error) {
	if m.groupsStore == nil {
		return nil, ErrGroupServiceUnavailable
	}
	exists, err := m.groupsStore.GroupExists(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrGroupNotFound
	}
	if err := m.groupsStore.Join(ctx, groupID, memberID); err != nil {
		return nil, err
	}
	// 同步内存: 未加载则懒加载 (含群历史窗口), 再 Join 返回窗口补发
	s := m.groups.Router(groupID)
	if !s.Loaded() {
		m.EnsureLoaded(ctx, groupID, s)
	}
	return s.Join(memberID), nil
}

// LeaveGroup 退出群: 落库 + 同步内存
func (m *Manager) LeaveGroup(ctx context.Context, groupID, memberID string) error {
	if m.groupsStore == nil {
		return ErrGroupServiceUnavailable
	}
	if err := m.groupsStore.Leave(ctx, groupID, memberID); err != nil {
		return err
	}
	m.groups.Router(groupID).Leave(memberID)
	return nil
}

// GroupMembers 群成员列表
func (m *Manager) GroupMembers(ctx context.Context, groupID string) ([]string, error) {
	if m.groupsStore == nil {
		return nil, ErrGroupServiceUnavailable
	}
	return m.groupsStore.Members(ctx, groupID)
}

// MyGroups 返回用户加入的群 ID 列表
func (m *Manager) MyGroups(ctx context.Context, memberID string) ([]string, error) {
	if m.groupsStore == nil {
		return nil, ErrGroupServiceUnavailable
	}
	return m.groupsStore.GroupsOf(ctx, memberID)
}

// IsMember 判断用户是否群成员 (内存查询, 群消息发送前/查看前鉴权)
// 群模型在应用启动后初始化到内存 (EnsureAllLoaded), 运行期直接查内存
func (m *Manager) IsMember(groupID, subject string) bool {
	s := m.groups.Get(groupID)
	if s == nil {
		return false
	}
	return s.Contains(subject)
}

// EnsureAllLoaded 应用启动初始化: 从 DB 加载所有群及成员到内存
// 之后群消息发送/成员鉴权均查内存; 新增成员先写 DB 后入内存 (原子, 见 JoinGroup)
func (m *Manager) EnsureAllLoaded(ctx context.Context) {
	if m.groupsStore == nil {
		slog.Warn("群持久化未接入, 跳过启动初始化")
		return
	}
	groups, err := m.groupsStore.ListGroups(ctx)
	if err != nil {
		slog.Warn("启动初始化群列表失败", slog.String("error", err.Error()))
		return
	}
	for _, g := range groups {
		s := m.groups.Router(g.GroupID)
		members, err := m.groupsStore.Members(ctx, g.GroupID)
		if err != nil {
			slog.Warn("启动初始化群成员失败", slog.String("group", g.GroupID), slog.String("error", err.Error()))
			continue
		}
		s.ReplaceMembers(members)
		s.MarkLoaded()
	}
	slog.Info("群模型初始化完成", slog.Int("groups", len(groups)))
}

// EnsureLoaded 懒加载群: 从持久层恢复成员与群历史窗口 (进程重启后首次访问)
// 持久层不可用时仅标记已加载 (避免每次投递都尝试加载), 降级为内存态
func (m *Manager) EnsureLoaded(ctx context.Context, gid string, s *GroupState) {
	if m.groupsStore == nil {
		s.MarkLoaded()
		return
	}
	members, err := m.groupsStore.Members(ctx, gid)
	if err != nil {
		slog.Warn("懒加载群成员失败", slog.String("group", gid), slog.String("error", err.Error()))
		return
	}
	s.ReplaceMembers(members)

	// 恢复群历史窗口 (仅窗口为空时, 避免覆盖实时窗口)
	if m.messages != nil && s.WindowEmpty() {
		rows, err := m.messages.FetchGroupHistory(ctx, gid, windowSize)
		if err != nil {
			slog.Warn("懒加载群历史失败", slog.String("group", gid), slog.String("error", err.Error()))
			return
		}
		msgs := make([]message.Message, 0, len(rows))
		for _, row := range rows {
			if mm, err := message.Unmarshal(row.Payload); err == nil {
				msgs = append(msgs, mm)
			}
		}
		s.ReplaceWindow(msgs)
	}
}
