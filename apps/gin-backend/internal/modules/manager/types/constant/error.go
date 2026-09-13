package constant

import "errors"

var (
	// ErrUsernameTaken 用户名已被占用 (已是管理员 或 有待审批/已通过的申请)
	ErrUsernameTaken = errors.New("manager: 用户名已被占用")
	// ErrRequestNotFound 申请单不存在
	ErrRequestNotFound = errors.New("manager: 申请单不存在")
	// ErrRequestReviewed 申请单已审批 (重复审批)
	ErrRequestReviewed = errors.New("manager: 申请单已审批")
)
