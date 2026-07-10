package utils

import (
	"app/internal/types"
)

// Response 是一个辅助函数，用于生成统一的 LoginResp 响应结构
func Response(
	code int, message string,
	accessToken, refreshToken string, expiresIn int64,
	withUserInfo func(*types.LoginResp),
) *types.LoginResp {
	var a, r string
	var exp int64
	if code == 200 {
		a = accessToken
		r = refreshToken
		exp = expiresIn
	}
	loginResp := &types.LoginResp{
		BaseResp: types.BaseResp{
			Code:    code,
			Message: message,
		},
		LoginRespBody: types.LoginRespBody{
			AccessToken:  a,
			RefreshToken: r,
			ExpiresIn:    exp,
		},
	}

	if withUserInfo != nil {
		withUserInfo(loginResp)
	}

	return loginResp
}

func WithUserInfo(userInfo *types.UserInfo) func(*types.LoginResp) {
	return func(loginResp *types.LoginResp) {
		loginResp.LoginRespBody.UserInfo = *userInfo
	}
}