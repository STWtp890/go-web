// jwt 包方法级定义: token 提取 / 解析 / claims 提取 (收敛自分散方法级文件)
// rediscache.go 保留独立 (Redis token 状态管理)
package jwt

import (
	"crypto/rsa"
	"errors"
	"strconv"
	"strings"

	"gin-backend/internal/config"

	"github.com/gin-gonic/gin"
	jwtlib "github.com/golang-jwt/jwt/v5"
)

// ExtractToken 从请求中提取 JWT Token
// :Param
// - c: gin.Context
// :Return
// - string: 提取到的 JWT Token，如果未提供则返回空字符串
func ExtractToken(c *gin.Context) string {
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" {
		return ""
	}

	tokenStr := strings.TrimPrefix(authHeader, "Bearer")
	if tokenStr == "" {
		return ""
	}

	return strings.TrimSpace(tokenStr)
}

// ExtractClaims 从 gin.Context 中提取 JWT Claims
// 中间件 (AuthRequired/ManagerAuthRequired) 注入的是 jwtlib.MapClaims 值类型,
// 此处值断言后返回指针, 保持调用方接口兼容
func ExtractClaims(c *gin.Context) (*jwtlib.MapClaims, bool) {
	claims, exists := c.Get("claims") // 此处 claims 是值类型, 需要断言为 jwtlib.MapClaims
	if !exists {
		return nil, false
	}

	mapClaims, ok := claims.(jwtlib.MapClaims)
	if !ok {
		return nil, false
	}

	return &mapClaims, true
}

// Subject 从 gin.Context 的 JWT claims 提取 subject (sub)
// 便捷封装: ExtractClaims + GetSubject, 供业务 handler 直接获取当前主体标识 (users.id / managers.id)
func Subject(c *gin.Context) (string, bool) {
	claims, ok := ExtractClaims(c)
	if !ok {
		return "", false
	}
	sub, err := claims.GetSubject()
	if err != nil || sub == "" {
		return "", false
	}
	return sub, true
}

// ParseSubject 将 JWT sub (数字字符串) 解析为 uint (users.id / managers.id)
func ParseSubject(sub string) (uint, bool) {
	id, err := strconv.ParseUint(sub, 10, 64)
	if err != nil {
		return 0, false
	}
	return uint(id), true
}

// SubjectUint 从 gin.Context 提取 subject 并解析为 uint
// 便捷封装: Subject + ParseSubject, 供业务 handler 直接获取当前主体 uint 标识
func SubjectUint(c *gin.Context) (uint, bool) {
	sub, ok := Subject(c)
	if !ok {
		return 0, false
	}
	return ParseSubject(sub)
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
