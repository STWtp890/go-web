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
	"golang.org/x/crypto/bcrypt"
)

type LoginLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewLoginLogic(ctx context.Context, svcCtx *svc.ServiceContext) *LoginLogic {
	return &LoginLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *LoginLogic) Login(req *types.LoginReq) (resp *types.LoginResp, err error) {
	// User Validation Phase — 支持邮箱或用户ID登录
	// 优先尝试邮箱查找，失败则尝试 userId 查找
	user, err := l.svcCtx.CustomCtx.SQLModel.UsersModel.FindOneByEmail(l.ctx, req.UserEmail)
	if err != nil {
		// 邮箱未匹配，尝试作为 userId 查找
		user, err = l.svcCtx.CustomCtx.SQLModel.UsersModel.FindOneByUserId(l.ctx, req.UserEmail)
		if err != nil {
			l.Logger.Errorf("Failed to find user: credential=%s, err=%v", req.UserEmail, err)
			return utils.Response(401, MsgInvalidCredentials, "", "", 0, nil), nil
		}
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.UserPassword)); err != nil {
		l.Logger.Errorf("Incorrect password for user: userId=%s", user.UserId)
		return utils.Response(401, "Invalid email/userId or password", "", "", 0, nil), nil
	}

	// 查询用户角色 — 通过 UserRolesModel.FindOneByUserId (UUID) 获取角色的 role_id (int64)
	userRole, err := l.svcCtx.CustomCtx.SQLModel.UserRolesModel.FindOneByUserId(l.ctx, user.UserId)
	if err != nil {
		l.Logger.Errorf("Failed to find user role: userId=%s, err=%v", user.UserId, err)
		// 无角色返回 403 错误，提示用户联系管理员
		return utils.Response(403, MsgNoRoleAssigned, "", "", 0, nil), nil
	}

	roleId := userRole.RoleId

	// JWT token generate phase — 使用对象ID (UUIDv7) 作为 JWT 中的 userId，并注入 roleId
	accessToken, err := utils.GenToken(
		user.UserId,
		roleId,
		common.Now().Unix(),
		l.svcCtx.Config.Auth.AccessExpire,
		l.svcCtx.Config.Auth.AccessSecret,
	)
	if err != nil {
		l.Logger.Errorf("Failed to generate access token: %v", err)
		return utils.Response(500, MsgGenAccessTokenFailed, "", "", 0, nil), err
	}

	// Redis Refresh Token Phase — key 使用对象ID (UUIDv7 字符串)
	redisKey := "refresh_token:" + user.UserId

	refreshToken, err := utils.GenToken(
		user.UserId,
		roleId,
		common.Now().Unix(),
		l.svcCtx.Config.Refresh.AccessExpire,
		l.svcCtx.Config.Refresh.AccessSecret,
	)
	if err != nil {
		l.Logger.Errorf("Failed to generate refresh token: %v", err)
		return utils.Response(500, MsgGenRefreshTokenFailed, "", "", 0, nil), err
	}

	var redisExpire int
	if l.svcCtx.Config.Refresh.AccessExpire <= math.MaxInt {
		redisExpire = int(l.svcCtx.Config.Refresh.AccessExpire)
	} else {
		redisExpire = math.MaxInt
		l.Slowf("The Refresh.AccessExpire value exceeds the maximum int value, setting redisExpire to MaxInt")
	}

	err = l.svcCtx.CustomCtx.Redis.Setex(redisKey, refreshToken, redisExpire)
	if err != nil {
		l.Logger.Errorf("Failed to set refresh token in Redis: %v", err)
		return utils.Response(500, MsgRedisSetFailed, "", "", 0, nil), err
	}

	expiresIn := l.svcCtx.Config.Auth.AccessExpire

	return utils.Response(200, MsgLoginSuccess, accessToken, refreshToken, expiresIn,
		utils.WithUserInfo(&types.UserInfo{
			UserId:   user.UserId,
			UserName: user.Username,
			Email:    user.Email,
			RoleId:   roleId,
		})), nil
}
