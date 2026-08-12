package jwtmethod

import (
	"errors"

	"github.com/golang-jwt/jwt/v5"
)

// ParseTokenClaims 验签 JWT 并提取 MapClaims
// 只接受 RS256（SigningMethodRSA）签名的 token
func ParseTokenClaims(tokenStr string, publicKey any) (*jwt.MapClaims, error) {
	token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, errors.New("不支持的签名算法")
		}
		return publicKey, nil
	})
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return nil, errors.New("token claims 无效")
	}

	return &claims, nil
}
