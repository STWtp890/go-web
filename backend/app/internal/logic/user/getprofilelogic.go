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

type GetProfileLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewGetProfileLogic(ctx context.Context, svcCtx *svc.ServiceContext) *GetProfileLogic {
	return &GetProfileLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *GetProfileLogic) GetProfile(req *types.UserProfileReq) (resp *types.UserProfileResp, err error) {
	// 校验 JWT userId 与路径 userId 一致，禁止越权查他人资料
	jwtUserId, err := common.GetUserIdFromCtx(l.ctx)
	if err != nil || jwtUserId != req.UserId {
		return utils.ProfileResponse(403, MsgForbidden, "", "", ""), nil
	}

	user, err := l.svcCtx.CustomCtx.SQLModel.UsersModel.FindOneByUserId(l.ctx, req.UserId)
	if err != nil {
		l.Logger.Errorf("Failed to find user: userId=%s, err=%v", req.UserId, err)
		return utils.ProfileResponse(404, MsgNotFound, "", "", ""), nil
	}

	resp = utils.ProfileResponse(200, MsgSuccess, user.UserId, user.Username, user.Email)
	return resp, nil
}
