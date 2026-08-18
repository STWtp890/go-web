package requests

// RegisterRequest 用户注册请求参数
type RegisterRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=6,max=128"`
	Nickname string `json:"nickname" binding:"required,min=3,max=32"`
}
