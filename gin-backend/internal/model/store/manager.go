// manager.go — 管理员实体缓存 (manager 业务)
//
// 查询键: 按主键 id / 唯一用户名 username 双维度, 登录/刷新场景按 username 回源。
// 审批通过创建管理员 / 停用账号后调用 Evict 失效, 保证下次读取回源新数据。
package store

import (
	"context"
	"fmt"

	basecache "gin-backend/internal/common/base/cache"
	dtocache "gin-backend/internal/model/cache"
	managermodel "gin-backend/internal/model/orm/manager"

	"gorm.io/gorm"
)

// 管理员缓存键命名空间
const (
	managerKeyByID       = "cache:manager:id:%d"
	managerKeyByUsername = "cache:manager:username:%s"
)

// ManagerStore 管理员实体缓存存储器
type ManagerStore struct {
	byID       *basecache.EntityCache[dtocache.ManagerCache]
	byUsername *basecache.EntityCache[dtocache.ManagerCache]
}

// Manager 管理员实体缓存包级单例
var Manager = &ManagerStore{
	byID:       NewEntity[dtocache.ManagerCache](DefaultTTL),
	byUsername: NewEntity[dtocache.ManagerCache](DefaultTTL),
}

// GetByID 按主键读取管理员 (透传 gorm.ErrRecordNotFound)
func (s *ManagerStore) GetByID(ctx context.Context, id uint) (*dtocache.ManagerCache, error) {
	key := fmt.Sprintf(managerKeyByID, id)
	m, err := s.byID.Get(ctx, key, func(ctx context.Context) (dtocache.ManagerCache, error) {
		db, err := authDB.get(ctx)
		if err != nil {
			return dtocache.ManagerCache{}, err
		}
		var row managermodel.Manager
		if err := db.Where("id = ?", id).First(&row).Error; err != nil {
			return dtocache.ManagerCache{}, err
		}
		return *dtocache.FromManager(&row), nil
	})
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// GetByUsername 按唯一用户名读取管理员 (登录/刷新校验场景)
func (s *ManagerStore) GetByUsername(ctx context.Context, username string) (*dtocache.ManagerCache, error) {
	key := fmt.Sprintf(managerKeyByUsername, username)
	m, err := s.byUsername.Get(ctx, key, func(ctx context.Context) (dtocache.ManagerCache, error) {
		db, err := authDB.get(ctx)
		if err != nil {
			return dtocache.ManagerCache{}, err
		}
		var row managermodel.Manager
		if err := db.Where("username = ?", username).First(&row).Error; err != nil {
			return dtocache.ManagerCache{}, err
		}
		return *dtocache.FromManager(&row), nil
	})
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// Evict 失效管理员缓存 (id 维度 + username 维度)
func (s *ManagerStore) Evict(ctx context.Context, id uint, username string) error {
	_ = s.byID.Evict(ctx, fmt.Sprintf(managerKeyByID, id))
	if username != "" {
		_ = s.byUsername.Evict(ctx, fmt.Sprintf(managerKeyByUsername, username))
	}
	return nil
}

// 编译期断言: ManagerStore 依赖的 gorm 错误由调用方经 errors.Is 判断
var _ = gorm.ErrRecordNotFound
