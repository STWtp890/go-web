package handler

import (
	"gin-backend/internal/common/service/jwtmethod"

	"github.com/gin-gonic/gin"
)

// currentSubject 从 JWT claims 提取当前用户标识 (sub)
// :Return
// - `string` 用户 ID
// - `bool` 是否提取成功
func currentSubject(c *gin.Context) (string, bool) {
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
