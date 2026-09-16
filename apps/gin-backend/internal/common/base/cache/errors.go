package cache

import "errors"

// ErrMiss 缓存未命中 (key 不存在或已过期)
var ErrMiss = errors.New("cache: miss")

// ErrEntryTooLarge 表示单条值超过进程内回退缓存的字节预算。
var ErrEntryTooLarge = errors.New("cache: entry too large")
