package jwt

import (
	"crypto/rsa"
	"errors"
)

// RefreshIdentity 是 refresh token 验签通过后可信的最小身份与会话信息。
//
// HTTP 适配层持有该类型后即可完成刷新编排, 无需接触 MapClaims;
// 因此 logic/application 层不再需要解析 token。
type RefreshIdentity struct {
	Subject   string
	UserID    uint
	SessionID string
	Role      string
	Email     string
	Nickname  string
}

// TokenPair 是一次刷新签发的新凭据对。
//
// 取代早期的 map[string]string: 键名拼写错误会立刻成为编译错误,
// 而不是静默得到空字符串。
type TokenPair struct {
	AccessToken  string
	RefreshToken string
}

// VerifyRefreshToken 验签 refresh token 并返回类型化身份。
//
// 这是 token 解析的唯一入口之一, 只允许 HTTP 适配层与 common/service/* 调用;
// 业务层 (logic / application / domain) 一律禁止。
func VerifyRefreshToken(tokenStr string, publicKey *rsa.PublicKey) (RefreshIdentity, error) {
	claims, err := ParseTokenClaims(tokenStr, publicKey, TokenUseRefresh)
	if err != nil {
		return RefreshIdentity{}, err
	}
	subject, err := claims.GetSubject()
	if err != nil || subject == "" {
		return RefreshIdentity{}, errors.New("refresh token 缺少主体")
	}
	sessionID, ok := SessionIDFromClaims(*claims)
	if !ok {
		return RefreshIdentity{}, errors.New("refresh token 缺少会话信息")
	}
	userID, ok := ParseSubject(subject)
	if !ok {
		return RefreshIdentity{}, errors.New("refresh token 主体非数字")
	}

	role, _ := (*claims)["role"].(string)
	email, _ := (*claims)["email"].(string)
	nickname, _ := (*claims)["nickname"].(string)

	return RefreshIdentity{
		Subject:   subject,
		UserID:    userID,
		SessionID: sessionID,
		Role:      role,
		Email:     email,
		Nickname:  nickname,
	}, nil
}
