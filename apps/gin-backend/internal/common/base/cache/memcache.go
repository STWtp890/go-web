// memstore.go — 进程内内存键值缓存存储 (MemCache 适配 Store 接口)
//
// 用途: 实体缓存的回退存储 (Redis 不可用/故障时降级), 单进程内共享。
// 与 RedisStore 组合: 主存储 Redis 未命中或故障时查内存, 命中后回填 Redis。
package cache

import (
	"container/list"
	"context"
	"fmt"
	"sync"
	"time"
)

// cacheEntry 缓存条目
type cacheEntry struct {
	val     string
	expAt   time.Time
	size    int64
	element *list.Element
}

type MemCacheOptions struct {
	MaxEntries    int
	MaxBytes      int64
	SweepInterval time.Duration
}

type MemCacheStats struct {
	Entries   int    `json:"entries"`
	Bytes     int64  `json:"bytes"`
	Hits      uint64 `json:"hits"`
	Misses    uint64 `json:"misses"`
	Expired   uint64 `json:"expired"`
	Evictions uint64 `json:"evictions"`
}

// MemCache 是带 TTL、LRU 和条目/字节双上限的进程内回退缓存。
type MemCache struct {
	mu            sync.Mutex
	data          map[string]*cacheEntry
	order         *list.List
	maxEntries    int
	maxBytes      int64
	sweepInterval time.Duration
	lastSweep     time.Time
	bytes         int64
	hits          uint64
	misses        uint64
	expired       uint64
	evictions     uint64
}

func newMemCache(options MemCacheOptions) *MemCache {
	if options.MaxEntries <= 0 {
		options.MaxEntries = 1
	}
	if options.MaxBytes <= 0 {
		options.MaxBytes = 1
	}
	if options.SweepInterval <= 0 {
		options.SweepInterval = time.Minute
	}
	return &MemCache{
		data: make(map[string]*cacheEntry), order: list.New(),
		maxEntries: options.MaxEntries, maxBytes: options.MaxBytes,
		sweepInterval: options.SweepInterval, lastSweep: time.Now(),
	}
}

func (c *MemCache) Get(_ context.Context, key string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, exists := c.data[key]
	if !exists {
		c.misses++
		return "", ErrMiss
	}
	if !e.expAt.IsZero() && time.Now().After(e.expAt) {
		c.removeLocked(key, e)
		c.expired++
		c.misses++
		return "", ErrMiss
	}
	c.order.MoveToFront(e.element)
	c.hits++
	return e.val, nil
}

func (c *MemCache) Set(_ context.Context, key, val string, ttl time.Duration) error {
	size := int64(len(key) + len(val))
	if size > c.maxBytes {
		return fmt.Errorf("%w: bytes=%d limit=%d", ErrEntryTooLarge, size, c.maxBytes)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	if now.Sub(c.lastSweep) >= c.sweepInterval {
		c.purgeExpiredLocked(now)
		c.lastSweep = now
	}
	if previous, exists := c.data[key]; exists {
		c.removeLocked(key, previous)
	}
	var expAt time.Time
	if ttl > 0 {
		expAt = now.Add(ttl)
	}
	element := c.order.PushFront(key)
	c.data[key] = &cacheEntry{val: val, expAt: expAt, size: size, element: element}
	c.bytes += size
	for len(c.data) > c.maxEntries || c.bytes > c.maxBytes {
		oldest := c.order.Back()
		if oldest == nil {
			break
		}
		oldestKey := oldest.Value.(string)
		c.removeLocked(oldestKey, c.data[oldestKey])
		c.evictions++
	}
	return nil
}

func (c *MemCache) Del(_ context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, exists := c.data[key]; exists {
		c.removeLocked(key, entry)
	}
	return nil
}

func (c *MemCache) Len() int {
	return c.Stats().Entries
}

func (c *MemCache) Stats() MemCacheStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.purgeExpiredLocked(time.Now())
	return MemCacheStats{
		Entries: len(c.data), Bytes: c.bytes, Hits: c.hits, Misses: c.misses,
		Expired: c.expired, Evictions: c.evictions,
	}
}

func (c *MemCache) purgeExpiredLocked(now time.Time) {
	for key, entry := range c.data {
		if !entry.expAt.IsZero() && now.After(entry.expAt) {
			c.removeLocked(key, entry)
			c.expired++
		}
	}
}

func (c *MemCache) removeLocked(key string, entry *cacheEntry) {
	if entry == nil {
		return
	}
	delete(c.data, key)
	c.order.Remove(entry.element)
	c.bytes -= entry.size
}
