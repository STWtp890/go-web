// Code scaffolded by goctl. Safe to edit.
// goctl 1.10.1

package auth

import (
	"context"
	"math"

	"app/internal/common"
	"app/internal/logic/auth/utils"
	"app/internal/svc"
	"app/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type RefreshTokenLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewRefreshTokenLogic(ctx context.Context, svcCtx *svc.ServiceContext) *RefreshTokenLogic {
	return &RefreshTokenLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// refreshTokenCheckAndDel 原子性地校验并删除 refresh token，
// 防止并发刷新导致的竞态条件（token 重放攻击）。
// 返回值：>0 表示校验成功并已删除；0 表示 token 不匹配或已被消费。
const refreshTokenCheckAndDel = `
if redis.call("GET", KEYS[1]) == ARGV[1] then
    return redis.call("DEL", KEYS[1])
else
    return 0
end`

func (l *RefreshTokenLogic) RefreshToken(req *types.RefreshReq) (resp *types.LoginResp, err error) {
	// 从 context 提取 userId - JWT 中间件已验证 refreshToken 签名
	userId, err := common.GetUserIdFromCtx(l.ctx)
	if err != nil {
		l.Logger.Errorf("Failed to get userId from context: %v", err)
		return utils.Response(401, MsgMissingUserId, "", "", 0, nil), nil
	}

	// Redis 原子校验：使用 Lua 脚本校验 refreshToken 并立即删除，
	// 避免并发请求同时通过校验（token 重放攻击）。
	redisKey := common.RefreshTokenRedisKey(userId)
	val, err := l.svcCtx.CustomCtx.Redis.Eval(refreshTokenCheckAndDel, []string{redisKey}, req.RefreshToken)
	if err != nil {
		l.Logger.Errorf("Failed to validate refresh token atomically: %v", err)
		return utils.Response(401, MsgTokenValidationFailed, "", "", 0, nil), nil
	}
	deleted, _ := val.(int64)
	if deleted == 0 {
		l.Logger.Errorf("Refresh token invalid or already consumed for userId=%s", userId)
		return utils.Response(401, MsgTokenInvalidOrUsed, "", "", 0, nil), nil
	}

	// 从旧 refreshToken 的 context 中提取 roleId（JWT 中间件注入）
	roleId, err := common.GetRoleIdFromCtx(l.ctx)
	if err != nil {
		l.Logger.Errorf("Failed to get roleId from context: %v", err)
		return utils.Response(401, MsgMissingRoleId, "", "", 0, nil), nil
	}

	// 签发新的 accessToken
	now := common.Now().Unix()
	accessToken, err := utils.GenToken(
		userId,
		roleId,
		now,
		l.svcCtx.Config.Auth.AccessExpire,
		l.svcCtx.Config.Auth.AccessSecret,
	)
	if err != nil {
		l.Logger.Errorf("Failed to generate access token: %v", err)
		return utils.Response(500, MsgGenAccessTokenFailed, "", "", 0, nil), err
	}

	// 签发新的 refreshToken
	refreshToken, err := utils.GenToken(
		userId,
		roleId,
		now,
		l.svcCtx.Config.Refresh.AccessExpire,
		l.svcCtx.Config.Refresh.AccessSecret,
	)
	if err != nil {
		l.Logger.Errorf("Failed to generate refresh token: %v", err)
		return utils.Response(500, MsgGenRefreshTokenFailed, "", "", 0, nil), err
	}

	// 更新 Redis 中的 refreshToken
	var redisExpire int
	if l.svcCtx.Config.Refresh.AccessExpire <= math.MaxInt {
		redisExpire = int(l.svcCtx.Config.Refresh.AccessExpire)
	} else {
		redisExpire = math.MaxInt
		l.Slowf("The Refresh.AccessExpire value exceeds the maximum int value, setting redisExpire to MaxInt")
	}
	err = l.svcCtx.CustomCtx.Redis.Setex(redisKey, refreshToken, redisExpire)
	if err != nil {
		l.Logger.Errorf("Failed to update refresh token in Redis: %v", err)
		return utils.Response(500, MsgRedisUpdateFailed, "", "", 0, nil), err
	}

	expiresIn := l.svcCtx.Config.Auth.AccessExpire
	return utils.Response(200, MsgTokenRefreshed, accessToken, refreshToken, expiresIn, nil), nil
}
