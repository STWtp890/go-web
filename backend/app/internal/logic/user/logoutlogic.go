// Code scaffolded by goctl. Safe to edit.
// goctl 1.10.1

package user

import (
	"context"

	"app/internal/common"
	"app/internal/logic/user/utils"
	"app/internal/svc"
	"app/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type LogoutLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewLogoutLogic(ctx context.Context, svcCtx *svc.ServiceContext) *LogoutLogic {
	return &LogoutLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *LogoutLogic) Logout(req *types.LogoutReq) (resp *types.LogoutResp, err error) {
	// 从路径参数提取 userId
	userIdFromPath := req.UserId

	// 从 context 提取 userId - JWT 中间件已验证 refreshToken 签名
	userIdFromJWT, err := common.GetUserIdFromCtx(l.ctx)
	if err != nil {
		l.Logger.Errorf("Failed to get userId from context: %v", err)
		return utils.LogoutResponse(401, "Invalid token!"), nil
	}
	
	// 检查路径参数中的 userId 是否与 JWT 中的 userId 匹配
	if userIdFromPath != userIdFromJWT {
		l.Logger.Errorf("UserId mismatch: path=%s, token=%s", userIdFromPath, userIdFromJWT)
		return utils.LogoutResponse(403, "UserId mismatch!"), nil
	}

	// Redis 删除 refreshToken
	redisKey := common.RefreshTokenRedisKey(userIdFromJWT)
	cmd, err := l.svcCtx.CustomCtx.Redis.Del(redisKey)
	if err != nil {
		l.Logger.Errorf("Failed to delete refresh token from Redis: %v", err)
		return utils.LogoutResponse(500, "Failed to logout"), err
	}

	l.Logger.Infof("User %s logged out successfully, Redis DEL result: %d", userIdFromJWT, cmd)
	return utils.LogoutResponse(200, "Logged out successfully"), nil
}
