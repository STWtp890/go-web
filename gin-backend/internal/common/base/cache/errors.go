package cache

import "errors"

// ErrMiss 缓存未命中 (key 不存在或已过期)
var ErrMiss = errors.New("cache: miss")
