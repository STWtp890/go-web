// Package cache 实现 document 当前版本详情的版本化两级缓存。
package cache

import (
	"context"
	"fmt"
	"time"

	basecache "gin-backend/internal/common/base/cache"
	"gin-backend/internal/modules/document/domain"
)

const DefaultTTL = 15 * time.Minute

type Cache struct {
	views *basecache.EntityCache[domain.DocumentView]
}

var _ domain.QueryCache = (*Cache)(nil)

func New(ttl time.Duration) *Cache {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &Cache{views: basecache.NewEntityCache[domain.DocumentView](
		basecache.NewRedisCache(), basecache.NewMemCache(), ttl,
	)}
}

func (cache *Cache) GetDocumentView(ctx context.Context, head domain.DocumentHead, loader domain.DocumentViewLoader) (*domain.DocumentView, error) {
	view, err := cache.views.Get(ctx, ViewKey(head), func(loadContext context.Context) (domain.DocumentView, error) {
		loaded, loadErr := loader(loadContext)
		if loadErr != nil {
			return domain.DocumentView{}, loadErr
		}
		return *loaded, nil
	})
	if err != nil {
		return nil, err
	}
	return &view, nil
}

// ViewKey 将活动版本和三类 fencing revision 编入缓存身份。
// 写事务提交后这些值发生变化，旧快照立即不可达并由 TTL 自然回收。
func ViewKey(head domain.DocumentHead) string {
	return fmt.Sprintf(
		"cache:document:v1:view:%s:%s:activation:%d:access:%d:lifecycle:%d",
		head.DocumentID, head.ActiveVersionID,
		head.ActivationRevision, head.AccessRevision, head.LifecycleRevision,
	)
}
