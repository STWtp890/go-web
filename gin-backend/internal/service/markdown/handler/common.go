package handler

import (
	"gin-backend/internal/common/service/jwtmethod"

	"github.com/gin-gonic/gin"
)

// currentUserID 从 JWT claims 提取当前用户标识 (sub = users.id)
// :Return
// - `string` 用户 ID
// - `bool` 是否提取成功
func currentUserID(c *gin.Context) (string, bool) {
	claims, ok := jwtmethod.ExtractClaims(c)
	if !ok {
		return "", false
	}
	sub, err := claims.GetSubject()
	if err != nil || sub == "" {
		return "", false
	}
	return sub, true
}

// normalizePage 归一化分页参数: page 至少 1, pageSize 落在 [1, 100]
func normalizePage(page, pageSize int) (int, int) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 10
	}
	return page, pageSize
}
