// manager 实体缓存对象
package cache

import "gin-backend/internal/model/orm/manager"

// ManagerCache 管理员缓存对象 (字段齐全, 含 Password)
type ManagerCache struct {
	ID        uint   `json:"id"`
	Username  string `json:"username"`
	Password  string `json:"password"` // 缓存需要, 不对外返回
	Email     string `json:"email"`
	Status    string `json:"status"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

// FromManager 由 ORM 管理员模型构造缓存对象
func FromManager(m *manager.Manager) *ManagerCache {
	return &ManagerCache{
		ID:        m.ID,
		Username:  m.Username,
		Password:  m.Password,
		Email:     m.Email,
		Status:    m.Status,
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
	}
}
