// Package cache 定义实体缓存对象 (缓存 DTO)
//
// 缓存对象与 ORM 模型解耦: 显式收编全部字段 (含 json:"-" 敏感字段),
// 避免复用 HTTP json tag 导致缓存序列化丢失字段 (如 User.Password / Markdown.SearchText)。
// 不包含 DeletedAt: 软删除对象应经 Evict 失效, 不入缓存。
package cache

import "gin-backend/internal/model/orm/auth"

// UserCache 用户缓存对象 (字段齐全, 含 Password)
type UserCache struct {
	ID        uint   `json:"id"`
	Email     string `json:"email"`
	Password  string `json:"password"` // 缓存需要, 不对外返回
	Nickname  string `json:"nickname"`
	Avatar    string `json:"avatar"`
	Banned    bool   `json:"banned"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

// FromUser 由 ORM 用户模型构造缓存对象
func FromUser(u *auth.User) *UserCache {
	return &UserCache{
		ID:        u.ID,
		Email:     u.Email,
		Password:  u.Password,
		Nickname:  u.Nickname,
		Avatar:    u.Avatar,
		Banned:    u.Banned,
		CreatedAt: u.CreatedAt,
		UpdatedAt: u.UpdatedAt,
	}
}
