package jwtmethod

import (
	"crypto/rsa"

	"github.com/golang-jwt/jwt/v5"
)

// ParseToken 解析 JWT Token（RS256 非对称验签）
func ParseToken(tokenStr string, publicKey *rsa.PublicKey) (*jwt.Token, error) {
	jwtToken, err := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
		return publicKey, nil
	})
	return jwtToken, err
}