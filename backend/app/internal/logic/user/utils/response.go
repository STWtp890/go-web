package utils

import (
	"app/internal/types"
)

// Response 是一个辅助函数，用于生成统一的 LoginResp 响应结构
func ProfileResponse(code int, message string, userId string, username, email string) *types.UserProfileResp {
	resp := &types.UserProfileResp{
		BaseResp: types.BaseResp{
			Code:    code,
			Message: message,
		},
		UserProfileRespBody: types.UserProfileRespBody{
			UserProfile: types.UserProfile{
				UserId:   userId,
				UserName: username,
				Email:    email,
			},
		},
	}
	return resp
}

func LogoutResponse(code int, message string) *types.LogoutResp {
	resp := &types.LogoutResp{
		BaseResp: types.BaseResp{
			Code:    code,
			Message: message,
		},
	}
	return resp
}