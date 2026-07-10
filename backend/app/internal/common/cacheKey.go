package common

import (
	"fmt"
)

// NewItemCacheKey 生成单个对象缓存的键
// field: 缓存的字段名，例如 "user"、"article" 等, 我们推荐与数据库表名保持一致(goctl model 风格)
// id: 对象的唯一标识符，例如用户 ID、文章 ID 等, 任意能保证唯一性的字符串都可以
func NewItemCacheKey(field string, id string) string {
	return fmt.Sprintf("cache:%s:%s", field, id)
}

// NewListCacheKey 生成列表缓存的键
// field: 缓存的字段名，例如 "user"、"article" 等, 我们推荐与数据库表名保持一致(goctl model 风格)
// pageNum: 页码
// pageSize: 每页大小
func NewListCacheKey(field string, pageNum, pageSize int) string {
	return fmt.Sprintf("cache:%s:list:%s", field, pageSizeFiled(pageNum, pageSize))
}

func pageSizeFiled(pageNum, pageSize int) string {
	return fmt.Sprintf("page=%d&size=%d", pageNum, pageSize)
}