package utils

import (
	"github.com/golang-jwt/jwt/v4"
)

// GenToken 使用 HS256 签发 JWT token
//   - userId:   存入 claims["userId"]
//   - roleId:   存入 claims["roleId"]
//   - iat:      签发时间（Unix 秒）
//   - seconds:  有效时长（秒），exp = iat + seconds
//   - secretKey: HMAC-SHA256 签名密钥
func GenToken(userId string, roleId, iat, seconds int64, secretKey string) (string, error) {
	claims := make(jwt.MapClaims)
	claims["userId"] = userId
	claims["roleId"] = roleId
	claims["iat"] = iat
	claims["exp"] = iat + seconds

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secretKey))
}
