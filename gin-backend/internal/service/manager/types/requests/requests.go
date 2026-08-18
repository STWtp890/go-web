// Package requests 定义 manager 业务请求参数
package requests

// RegisterRequest 提交管理员注册申请
// :Field
// - Username: 登录名
// - Password: 登录密码 (bcrypt 存储)
// - Email: 联系邮箱 (可选)
// - Reason: 申请理由 (可选)
type RegisterRequest struct {
	Username string `json:"username" binding:"required,min=3,max=64"`
	Password string `json:"password" binding:"required,min=6,max=128"`
	Email    string `json:"email" binding:"omitempty,email"`
	Reason   string `json:"reason" binding:"omitempty,max=512"`
}

// LoginRequest 管理员登录
type LoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// ListRequestQuery 审批列表查询参数
type ListRequestQuery struct {
	Page     int `form:"page" binding:"omitempty,min=1"`
	PageSize int `form:"pageSize" binding:"omitempty,min=1,max=100"`
}

// ReviewRequest 审批操作请求 (意见可选)
type ReviewRequest struct {
	Comment string `json:"comment" binding:"omitempty,max=512"`
}
