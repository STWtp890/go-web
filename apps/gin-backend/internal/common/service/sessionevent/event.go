// Package sessionevent 定义会话撤销事件及其发布/订阅基础设施。
//
// 事件用于通知业务模块关闭连接或释放会话资源；Token 是否有效仍由 JWT sid
// 与 Redis 当前会话状态的同步校验决定，不能依赖事件投递结果。
package sessionevent

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// EventType 是会话生命周期事件类型。
type EventType string

const (
	// EventSessionRevoked 表示指定会话已不再有效。
	EventSessionRevoked EventType = "session.revoked"
)

// PrincipalType 标识会话主体类别。
type PrincipalType string

const (
	// PrincipalUser 是普通用户主体。
	PrincipalUser PrincipalType = "user"
)

// RevokeReason 描述会话被撤销的原因。
type RevokeReason string

const (
	RevokeReasonLoginReplaced RevokeReason = "login_replaced"
	RevokeReasonLogout        RevokeReason = "logout"
	RevokeReasonAdminForced   RevokeReason = "admin_forced"
	RevokeReasonPasswordReset RevokeReason = "password_reset"
	RevokeReasonAccountBanned RevokeReason = "account_banned"
	RevokeReasonRefreshReplay RevokeReason = "refresh_replay"
)

// SessionRevokedEvent 是业务模块可订阅的会话撤销通知。
// SessionID 必须为被撤销的 sid，而不是新登录产生的 sid。
type SessionRevokedEvent struct {
	EventID       string        `json:"event_id"`
	Type          EventType     `json:"type"`
	PrincipalType PrincipalType `json:"principal_type"`
	PrincipalID   string        `json:"principal_id"`
	SessionID     string        `json:"session_id"`
	Reason        RevokeReason  `json:"reason"`
	OccurredAt    time.Time     `json:"occurred_at"`
}

// NewUserSessionRevokedEvent 构造普通用户的会话撤销事件。
func NewUserSessionRevokedEvent(userID, sessionID string, reason RevokeReason) SessionRevokedEvent {
	return SessionRevokedEvent{
		EventID:       uuid.NewString(),
		Type:          EventSessionRevoked,
		PrincipalType: PrincipalUser,
		PrincipalID:   userID,
		SessionID:     sessionID,
		Reason:        reason,
		OccurredAt:    time.Now().UTC(),
	}
}

// Validate 校验事件字段，避免将无法被安全处理的数据发送到总线。
func (e SessionRevokedEvent) Validate() error {
	if e.EventID == "" || e.PrincipalID == "" || e.SessionID == "" || e.Reason == "" {
		return errors.New("会话撤销事件字段不完整")
	}
	if e.Type != EventSessionRevoked {
		return errors.New("不支持的会话事件类型")
	}
	if e.PrincipalType != PrincipalUser {
		return errors.New("不支持的会话主体类型")
	}
	if e.OccurredAt.IsZero() {
		return errors.New("会话撤销事件缺少发生时间")
	}
	return nil
}
