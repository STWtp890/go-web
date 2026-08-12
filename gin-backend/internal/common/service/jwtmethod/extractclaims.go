package jwtmethod

import (
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// ExtractClaims 从 gin.Context 中提取 JWT Claims
func ExtractClaims(c *gin.Context) (*jwt.MapClaims, bool) {
	claims, exists := c.Get("claims")
	if !exists {
		return nil, false
	}

	mapClaims, ok := claims.(*jwt.MapClaims)
	if !ok {
		return nil, false
	}

	return mapClaims, true
}