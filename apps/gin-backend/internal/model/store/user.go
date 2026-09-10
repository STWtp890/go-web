// user.go — 用户实体缓存 (auth 业务)
//
// 查询键: 按主键 id / 唯一邮箱 email 双维度, 登录/刷新场景按 email 或 id 回源。
// 写库后 (注册/改密/封禁) 调用 Evict 失效两个维度, 保证下次读取回源新数据。
package store

import (
	"context"
	"fmt"

	basecache "gin-backend/internal/common/base/cache"
	dtocache "gin-backend/internal/model/cache"
	authmodel "gin-backend/internal/model/orm/auth"

	"gorm.io/gorm"
)

// 用户缓存键命名空间 (前缀 cache:user: 防跨实体冲突)
const (
	userKeyByID    = "cache:user:id:%d"
	userKeyByEmail = "cache:user:email:%s"
)

// UserStore 用户实体缓存存储器
type UserStore struct {
	byID    *basecache.EntityCache[dtocache.UserCache]
	byEmail *basecache.EntityCache[dtocache.UserCache]
}

// User 用户实体缓存包级单例 (与 jwt fallbackCache / chat hub 同风格)
var User = &UserStore{
	byID:    NewEntity[dtocache.UserCache](DefaultTTL),
	byEmail: NewEntity[dtocache.UserCache](DefaultTTL),
}

// GetByID 按主键读取用户 (缓存未命中回源 users.id, 透传 gorm.ErrRecordNotFound)
func (s *UserStore) GetByID(ctx context.Context, id uint) (*dtocache.UserCache, error) {
	key := fmt.Sprintf(userKeyByID, id)
	u, err := s.byID.Get(ctx, key, func(ctx context.Context) (dtocache.UserCache, error) {
		db, err := authDB.get(ctx)
		if err != nil {
			return dtocache.UserCache{}, err
		}
		var m authmodel.User
		if err := db.Where("id = ?", id).First(&m).Error; err != nil {
			return dtocache.UserCache{}, err
		}
		return *dtocache.FromUser(&m), nil
	})
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// GetByEmail 按唯一邮箱读取用户 (登录场景; 透传 gorm.ErrRecordNotFound)
func (s *UserStore) GetByEmail(ctx context.Context, email string) (*dtocache.UserCache, error) {
	key := fmt.Sprintf(userKeyByEmail, email)
	fn := func(ctx context.Context) (dtocache.UserCache, error) {
		db, err := authDB.get(ctx)
		if err != nil {
			return dtocache.UserCache{}, err
		}
		var m authmodel.User
		if err := db.Where("email = ?", email).First(&m).Error; err != nil {
			return dtocache.UserCache{}, err
		}
		return *dtocache.FromUser(&m), nil
	}
	u, err := s.byEmail.Get(ctx, key, fn)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// Evict 失效用户缓存 (id 维度 + email 维度)
// 业务层写库后调用: Evict(ctx, user.ID, user.Email)
func (s *UserStore) Evict(ctx context.Context, id uint, email string) error {
	_ = s.byID.Evict(ctx, fmt.Sprintf(userKeyByID, id))
	_ = s.byEmail.Evict(ctx, fmt.Sprintf(userKeyByEmail, email))
	return nil
}

// 编译期断言: UserStore 依赖的 gorm 错误由调用方经 errors.Is 判断
var _ = gorm.ErrRecordNotFound
