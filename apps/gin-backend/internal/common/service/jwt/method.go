// jwt 包方法级定义: token 验签与 claims 提取。
//
// 身份读取不属于本包职责: 鉴权中间件把解析结果收敛为
// platform/httpserver/identity.Principal 注入请求上下文, 业务层读取该类型即可,
// 因此本包不依赖 gin。
package jwt

import (
	"crypto/rsa"
	"errors"
	"strconv"

	"gin-backend/internal/config"

	jwtlib "github.com/golang-jwt/jwt/v5"
)

// ParseSubject 将 JWT sub (数字字符串) 解析为 uint (users.id / managers.id)
func ParseSubject(sub string) (uint, bool) {
	id, err := strconv.ParseUint(sub, 10, 64)
	if err != nil {
		return 0, false
	}
	return uint(id), true
}

// SessionIDFromClaims 从 JWT claims 提取会话标识 sid。
// 不含 sid 的历史 token 不具备当前会话语义，应由调用方拒绝。
func SessionIDFromClaims(claims jwtlib.MapClaims) (string, bool) {
	sid, ok := claims["sid"].(string)
	return sid, ok && sid != ""
}

// 算法白名单: 仅接受 RS256 (防 alg 混淆攻击: 拒绝 none / HS 对称 / 弱 RSA 变体)
var allowedSigningMethods = []string{jwtlib.SigningMethodRS256.Alg()}

const (
	TokenUseAccess  = "access"
	TokenUseRefresh = "refresh"
)

// ParseToken 解析 JWT Token（仅接受 RS256 非对称验签）
// 经 WithValidMethods 固定 alg 白名单: token 头声明其他算法直接拒绝
func ParseToken(tokenStr string, publicKey *rsa.PublicKey, expectedUse string) (*jwtlib.Token, error) {
	jwtToken, err := jwtlib.Parse(tokenStr,
		func(token *jwtlib.Token) (interface{}, error) {
			return publicKey, nil
		},
		jwtlib.WithValidMethods(allowedSigningMethods),
		jwtlib.WithIssuer(config.CustomConfig().JWT.Issuer),
	)
	if err != nil {
		return nil, err
	}
	claims, ok := jwtToken.Claims.(jwtlib.MapClaims)
	if !ok || !jwtToken.Valid || claims["token_use"] != expectedUse {
		return nil, errors.New("token 用途无效")
	}
	return jwtToken, err
}

// ParseTokenClaims 验签 JWT 并提取 MapClaims
// 双重校验: keyFunc 类型断言 SigningMethodRSA + WithValidMethods 固定 alg=RS256
func ParseTokenClaims(tokenStr string, publicKey any, expectedUse string) (*jwtlib.MapClaims, error) {
	token, err := jwtlib.Parse(tokenStr,
		func(t *jwtlib.Token) (any, error) {
			if _, ok := t.Method.(*jwtlib.SigningMethodRSA); !ok {
				return nil, errors.New("不支持的签名算法")
			}
			return publicKey, nil
		},
		jwtlib.WithValidMethods(allowedSigningMethods),
		jwtlib.WithIssuer(config.CustomConfig().JWT.Issuer),
	)
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(jwtlib.MapClaims)
	if !ok || !token.Valid {
		return nil, errors.New("token claims 无效")
	}
	if claims["token_use"] != expectedUse {
		return nil, errors.New("token 用途无效")
	}

	return &claims, nil
}
