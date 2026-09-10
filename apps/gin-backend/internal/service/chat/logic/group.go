// 群业务逻辑：经共享聊天服务提供加入、退出、查看与鉴权能力。
package logic

import (
	"context"
	"errors"

	"gin-backend/internal/service/chat"
)

// JoinGroupLogic 申请加入聊天室 (先写 DB 后入内存, 原子)
func JoinGroupLogic(ctx context.Context, subject, groupID string) error {
	service := chat.Service()
	if service == nil {
		return errors.New("chat service 未初始化")
	}
	return service.JoinGroup(ctx, groupID, subject)
}

// LeaveGroupLogic 退出聊天室
func LeaveGroupLogic(ctx context.Context, subject, groupID string) error {
	service := chat.Service()
	if service == nil {
		return errors.New("chat service 未初始化")
	}
	return service.LeaveGroup(ctx, groupID, subject)
}

// IsGroupMemberLogic 判断用户是否群成员 (内存查询, 供鉴权)
// 群模型在应用启动后初始化到内存, 发送前/查看前直接查内存
func IsGroupMemberLogic(subject, groupID string) bool {
	service := chat.Service()
	if service == nil {
		return false
	}
	return service.IsGroupMember(groupID, subject)
}

// GroupMembersLogic 群成员列表
func GroupMembersLogic(ctx context.Context, groupID string) ([]string, error) {
	service := chat.Service()
	if service == nil {
		return nil, errors.New("chat service 未初始化")
	}
	return service.GroupMembers(ctx, groupID)
}

// MyGroupsLogic 我的群列表
func MyGroupsLogic(ctx context.Context, subject string) ([]string, error) {
	service := chat.Service()
	if service == nil {
		return nil, errors.New("chat service 未初始化")
	}
	return service.MyGroups(ctx, subject)
}
