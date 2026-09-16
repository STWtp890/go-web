// manager.go — 管理员实体版本缓存 (manager 业务)
//
// ID 与用户名只用于定位权威 head；缓存统一按 (id, cache_revision) 建键。
package store

import (
	"context"
	"errors"
	"fmt"

	basecache "gin-backend/internal/common/base/cache"
	dtocache "gin-backend/internal/model/cache"
	managermodel "gin-backend/internal/model/orm/manager"

	"gorm.io/gorm"
)

const managerVersionKey = "cache:manager:v2:%d:revision:%d"

type managerCacheHead struct {
	ID            uint
	CacheRevision int64
}

// ManagerStore 管理员实体缓存存储器。ID 与用户名查询共享一个实体缓存。
type ManagerStore struct {
	entities *basecache.EntityCache[dtocache.ManagerCache]
}

// Manager 管理员实体缓存包级单例。
var Manager = &ManagerStore{
	entities: NewEntity[dtocache.ManagerCache]("manager", DefaultTTL),
}

// GetByID 按主键读取管理员。head 查询始终直达 PostgreSQL。
func (s *ManagerStore) GetByID(ctx context.Context, id uint) (*dtocache.ManagerCache, error) {
	return s.get(ctx, func(db *gorm.DB, head *managerCacheHead) error {
		return db.Model(&managermodel.Manager{}).
			Select("id", "cache_revision").
			Where("id = ?", id).
			Take(head).Error
	})
}

// GetByUsername 按唯一用户名读取管理员。用户名不进入缓存键。
func (s *ManagerStore) GetByUsername(ctx context.Context, username string) (*dtocache.ManagerCache, error) {
	return s.get(ctx, func(db *gorm.DB, head *managerCacheHead) error {
		return db.Model(&managermodel.Manager{}).
			Select("id", "cache_revision").
			Where("username = ?", username).
			Take(head).Error
	})
}

func (s *ManagerStore) get(
	ctx context.Context,
	loadHead func(db *gorm.DB, head *managerCacheHead) error,
) (*dtocache.ManagerCache, error) {
	for attempt := 0; attempt < revisionReadAttempts; attempt++ {
		db, err := authDB.get(ctx)
		if err != nil {
			return nil, err
		}
		var head managerCacheHead
		if err := loadHead(db, &head); err != nil {
			return nil, err
		}

		key := managerCacheKey(head.ID, head.CacheRevision)
		cached, err := s.entities.Get(ctx, key, func(ctx context.Context) (dtocache.ManagerCache, error) {
			var row managermodel.Manager
			err := db.WithContext(ctx).
				Where("id = ? AND cache_revision = ?", head.ID, head.CacheRevision).
				First(&row).Error
			if err != nil {
				return dtocache.ManagerCache{}, err
			}
			return *dtocache.FromManager(&row), nil
		})
		if err == nil {
			return &cached, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		// head 与实体读取之间发生了更新或删除；重读权威 head。
	}
	return nil, fmt.Errorf("%w: manager", ErrCacheRevisionChanged)
}

// Evict 删除指定 revision 的两级缓存。版本键保证正确性；删除只负责及时回收旧键。
func (s *ManagerStore) Evict(ctx context.Context, id uint, revision int64) error {
	return s.entities.Evict(ctx, managerCacheKey(id, revision))
}

func managerCacheKey(id uint, revision int64) string {
	return fmt.Sprintf(managerVersionKey, id, revision)
}
