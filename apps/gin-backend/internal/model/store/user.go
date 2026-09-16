// user.go — 用户实体版本缓存 (auth 业务)
//
// 每次读取先从 PostgreSQL 获取权威 (id, cache_revision) head，再以版本键读取缓存。
// 写事务提交后 revision 单调递增，因此旧请求即使延迟回填，也只会写入旧版本键。
package store

import (
	"context"
	"errors"
	"fmt"

	basecache "gin-backend/internal/common/base/cache"
	dtocache "gin-backend/internal/model/cache"
	authmodel "gin-backend/internal/model/orm/auth"

	"gorm.io/gorm"
)

const userVersionKey = "cache:user:v2:%d:revision:%d"

type userCacheHead struct {
	ID            uint
	CacheRevision int64
}

// UserStore 用户实体缓存存储器。ID 与邮箱查询最终收敛到同一个版本键空间。
type UserStore struct {
	entities *basecache.EntityCache[dtocache.UserCache]
}

// User 用户实体缓存包级单例。
var User = &UserStore{
	entities: NewEntity[dtocache.UserCache]("user", DefaultTTL),
}

// GetByID 按主键读取用户。head 查询始终直达 PostgreSQL。
func (s *UserStore) GetByID(ctx context.Context, id uint) (*dtocache.UserCache, error) {
	return s.get(ctx, func(db *gorm.DB, head *userCacheHead) error {
		return db.Model(&authmodel.User{}).
			Select("id", "cache_revision").
			Where("id = ?", id).
			Take(head).Error
	})
}

// GetByEmail 按唯一邮箱读取用户。邮箱只用于定位 head，不进入缓存键。
func (s *UserStore) GetByEmail(ctx context.Context, email string) (*dtocache.UserCache, error) {
	return s.get(ctx, func(db *gorm.DB, head *userCacheHead) error {
		return db.Model(&authmodel.User{}).
			Select("id", "cache_revision").
			Where("email = ?", email).
			Take(head).Error
	})
}

func (s *UserStore) get(
	ctx context.Context,
	loadHead func(db *gorm.DB, head *userCacheHead) error,
) (*dtocache.UserCache, error) {
	for attempt := 0; attempt < revisionReadAttempts; attempt++ {
		db, err := authDB.get(ctx)
		if err != nil {
			return nil, err
		}
		var head userCacheHead
		if err := loadHead(db, &head); err != nil {
			return nil, err
		}

		key := userCacheKey(head.ID, head.CacheRevision)
		cached, err := s.entities.Get(ctx, key, func(ctx context.Context) (dtocache.UserCache, error) {
			var row authmodel.User
			err := db.WithContext(ctx).
				Where("id = ? AND cache_revision = ?", head.ID, head.CacheRevision).
				First(&row).Error
			if err != nil {
				return dtocache.UserCache{}, err
			}
			return *dtocache.FromUser(&row), nil
		})
		if err == nil {
			return &cached, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		// head 与实体读取之间发生了更新或删除；重读权威 head。
	}
	return nil, fmt.Errorf("%w: user", ErrCacheRevisionChanged)
}

// Evict 删除指定 revision 的两级缓存。版本键保证正确性；删除只负责及时回收旧键。
func (s *UserStore) Evict(ctx context.Context, id uint, revision int64) error {
	return s.entities.Evict(ctx, userCacheKey(id, revision))
}

func userCacheKey(id uint, revision int64) string {
	return fmt.Sprintf(userVersionKey, id, revision)
}
