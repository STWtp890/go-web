// 群业务逻辑: 经 Bridge (共享消息桥) 提供加入/退出/查看与鉴权能力 (应用级聊天室)
package logic

import (
	"context"
	"errors"

	"gin-backend/internal/service/chat"
	msg "gin-backend/internal/service/chat/types/message"
)

// JoinGroupLogic 申请加入聊天室 (先写 DB 后入内存, 原子; 返回窗口消息供补发)
func JoinGroupLogic(ctx context.Context, subject, groupID string) ([]msg.Message, error) {
	h := chat.WebSocketHub()
	if h == nil {
		return nil, errors.New("chat Hub 未初始化")
	}
	return h.Bridge().JoinGroup(ctx, groupID, subject)
}

// LeaveGroupLogic 退出聊天室
func LeaveGroupLogic(ctx context.Context, subject, groupID string) error {
	h := chat.WebSocketHub()
	if h == nil {
		return errors.New("chat Hub 未初始化")
	}
	return h.Bridge().LeaveGroup(ctx, groupID, subject)
}

// IsGroupMemberLogic 判断用户是否群成员 (内存查询, 供鉴权)
// 群模型在应用启动后初始化到内存, 发送前/查看前直接查内存
func IsGroupMemberLogic(subject, groupID string) bool {
	h := chat.WebSocketHub()
	if h == nil {
		return false
	}
	return h.Bridge().IsGroupMember(groupID, subject)
}

// GroupMembersLogic 群成员列表
func GroupMembersLogic(ctx context.Context, groupID string) ([]string, error) {
	h := chat.WebSocketHub()
	if h == nil {
		return nil, errors.New("chat Hub 未初始化")
	}
	return h.Bridge().GroupMembers(ctx, groupID)
}

// MyGroupsLogic 我的群列表
func MyGroupsLogic(ctx context.Context, subject string) ([]string, error) {
	h := chat.WebSocketHub()
	if h == nil {
		return nil, errors.New("chat Hub 未初始化")
	}
	return h.Bridge().MyGroups(ctx, subject)
}
