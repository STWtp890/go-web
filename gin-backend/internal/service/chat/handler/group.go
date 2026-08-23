// 群 HTTP 入口: 仅提供加入/退出/查看 (我的聊天室/聊天室成员)
// 群 (应用级聊天室) 由应用/管理员创建, 用户不能自建群/删群, 可申请加入/退出
package handler

import (
	"errors"
	"net/http"

	eror "gin-backend/internal/common/base/errors"
	"gin-backend/internal/common/base/responses"
	logic "gin-backend/internal/service/chat/logic"
	"gin-backend/internal/service/chat/types/group"

	"github.com/gin-gonic/gin"
)

// JoinGroupHandler 申请加入聊天室: POST /api/v1/protected/chat/groups/:groupId/join
// 权限: 在库 (AuthRequired 校验 token → 系统注册用户) 即可申请, 加入后转变为"在群"
// 响应: {groupId, memberId}
func JoinGroupHandler(c *gin.Context) {
	subject, ok := currentSubject(c)
	if !ok {
		responses.Fail(c, http.StatusUnauthorized, eror.CodeUnauthorized, "无效的Token")
		return
	}
	groupID := c.Param("groupId")
	if groupID == "" {
		responses.Fail(c, http.StatusBadRequest, eror.CodeValidationFailed, "缺少群 ID")
		return
	}

	err := logic.JoinGroupLogic(c.Request.Context(), subject, groupID)
	if err != nil {
		if errors.Is(err, group.ErrGroupNotFound) {
			responses.Fail(c, http.StatusNotFound, eror.CodeNotFound, "聊天室不存在")
			return
		}
		if errors.Is(err, group.ErrGroupServiceUnavailable) {
			responses.Fail(c, http.StatusServiceUnavailable, eror.CodeServiceUnavail, err.Error())
			return
		}
		responses.Fail(c, http.StatusInternalServerError, eror.CodeInternalError, "加入聊天室失败")
		return
	}
	responses.OK(c, gin.H{
		"groupId":  groupID,
		"memberId": subject,
	})
}

// LeaveGroupHandler 退出聊天室: POST /api/v1/protected/chat/groups/:groupId/leave
// 权限: 在群 (群成员) 才可退出, 非成员返回 403
func LeaveGroupHandler(c *gin.Context) {
	subject, ok := currentSubject(c)
	if !ok {
		responses.Fail(c, http.StatusUnauthorized, eror.CodeUnauthorized, "无效的Token")
		return
	}
	groupID := c.Param("groupId")
	if groupID == "" {
		responses.Fail(c, http.StatusBadRequest, eror.CodeValidationFailed, "缺少群 ID")
		return
	}

	// 在群校验 (内存查询): 非成员无权退出
	if !logic.IsGroupMemberLogic(subject, groupID) {
		responses.Fail(c, http.StatusForbidden, eror.CodeForbidden, "非群成员, 无权操作")
		return
	}

	if err := logic.LeaveGroupLogic(c.Request.Context(), subject, groupID); err != nil {
		if errors.Is(err, group.ErrGroupServiceUnavailable) {
			responses.Fail(c, http.StatusServiceUnavailable, eror.CodeServiceUnavail, err.Error())
			return
		}
		responses.Fail(c, http.StatusInternalServerError, eror.CodeInternalError, "退出聊天室失败")
		return
	}
	responses.OK(c, gin.H{"groupId": groupID, "memberId": subject})
}

// GroupMembersHandler 聊天室成员列表: GET /api/v1/protected/chat/groups/:groupId/members
// 权限: 在群 (群成员) 才可查看 (内存成员校验)
func GroupMembersHandler(c *gin.Context) {
	subject, ok := currentSubject(c)
	if !ok {
		responses.Fail(c, http.StatusUnauthorized, eror.CodeUnauthorized, "无效的Token")
		return
	}
	groupID := c.Param("groupId")
	if groupID == "" {
		responses.Fail(c, http.StatusBadRequest, eror.CodeValidationFailed, "缺少群 ID")
		return
	}

	// 成员身份校验 (内存查询): 非成员无权查看
	if !logic.IsGroupMemberLogic(subject, groupID) {
		responses.Fail(c, http.StatusForbidden, eror.CodeForbidden, "非群成员, 无权查看")
		return
	}

	members, err := logic.GroupMembersLogic(c.Request.Context(), groupID)
	if err != nil {
		if errors.Is(err, group.ErrGroupServiceUnavailable) {
			responses.Fail(c, http.StatusServiceUnavailable, eror.CodeServiceUnavail, err.Error())
			return
		}
		responses.Fail(c, http.StatusInternalServerError, eror.CodeInternalError, "获取群成员失败")
		return
	}
	responses.OK(c, gin.H{"groupId": groupID, "members": members})
}

// MyGroupsHandler 我的聊天室列表: GET /api/v1/protected/chat/groups/mine
func MyGroupsHandler(c *gin.Context) {
	subject, ok := currentSubject(c)
	if !ok {
		responses.Fail(c, http.StatusUnauthorized, eror.CodeUnauthorized, "无效的Token")
		return
	}

	groups, err := logic.MyGroupsLogic(c.Request.Context(), subject)
	if err != nil {
		if errors.Is(err, group.ErrGroupServiceUnavailable) {
			responses.Fail(c, http.StatusServiceUnavailable, eror.CodeServiceUnavail, err.Error())
			return
		}
		responses.Fail(c, http.StatusInternalServerError, eror.CodeInternalError, "获取群列表失败")
		return
	}
	responses.OK(c, gin.H{"groups": groups})
}
