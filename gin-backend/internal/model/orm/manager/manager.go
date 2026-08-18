// Package manager 定义管理员账号与注册审批流持久化模型
package manager

import (
	"time"

	"gin-backend/internal/model/orm"
)

// 管理员账号状态
const (
	// ManagerActive 正常 (可登录)
	ManagerActive = "active"
	// ManagerDisabled 停用 (不可登录)
	ManagerDisabled = "disabled"
)

// 注册申请状态
const (
	// RequestPending 待审批
	RequestPending = "pending"
	// RequestApproved 已通过 (成为管理员)
	RequestApproved = "approved"
	// RequestRejected 已拒绝
	RequestRejected = "rejected"
)

// Manager 管理员账号
// :Field
// - Username: 登录名 (唯一)
// - Password: bcrypt 哈希
// - Status: active / disabled
type Manager struct {
	ID       uint   `json:"id" gorm:"primaryKey;autoIncrement"`
	Username string `json:"username" gorm:"size:64;uniqueIndex;not null"`
	Password string `json:"-" gorm:"size:128;not null"`
	Email    string `json:"email" gorm:"size:128;default:''"`
	Status   string `json:"status" gorm:"size:16;not null;default:'active'"`
	orm.TimeFiled
}

// TableName 指定表名
func (*Manager) TableName() string {
	return "managers"
}

// RegistrationRequest 管理员注册申请单
// :Field
// - Username: 申请登录名
// - PasswordHash: bcrypt 哈希 (审批通过时迁移到 managers)
// - Status: pending / approved / rejected
// - ReviewerID: 审批人 (managers.id)
// - ReviewComment: 审批意见 (拒绝理由等)
// - ReviewedAt: 审批时间
type RegistrationRequest struct {
	ID            uint       `json:"id" gorm:"primaryKey;autoIncrement"`
	Username      string     `json:"username" gorm:"size:64;not null"`
	PasswordHash  string     `json:"-" gorm:"size:128;not null"`
	Email         string     `json:"email" gorm:"size:128;default:''"`
	Reason        string     `json:"reason" gorm:"size:512;default:''"`
	Status        string     `json:"status" gorm:"size:16;not null;default:'pending'"`
	ReviewerID    uint       `json:"reviewer_id" gorm:"default:0"`
	ReviewComment string     `json:"review_comment" gorm:"size:512;default:''"`
	ReviewedAt    *time.Time `json:"reviewed_at"`
	orm.TimeFiled
}

// TableName 指定表名
func (*RegistrationRequest) TableName() string {
	return "manager_registration_requests"
}
