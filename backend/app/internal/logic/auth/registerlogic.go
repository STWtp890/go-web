// Code scaffolded by goctl. Safe to edit.
// goctl 1.10.1

package auth

import (
	"context"

	"app/internal/common"
	"app/internal/model/auth"
	"app/internal/model/rbac"
	"app/internal/svc"
	"app/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
	"golang.org/x/crypto/bcrypt"
)

type RegisterLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewRegisterLogic(ctx context.Context, svcCtx *svc.ServiceContext) *RegisterLogic {
	return &RegisterLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *RegisterLogic) Register(req *types.RegisterReq) (resp *types.RegisterResp, err error) {

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.RegisterInfo.UserPassword), bcrypt.DefaultCost)
	if err != nil {
		l.Logger.Errorf("Failed to hash password: %v", err)
		return &types.RegisterResp{
			BaseResp: types.BaseResp{
				Code:    500,
				Message: MsgProcessPwdFailed,
			},
		}, err
	}

	// 自动生成 UUIDv7
	userId := common.MustUUIDv7()

	result, err := l.svcCtx.CustomCtx.SQLModel.UsersModel.Insert(
		l.ctx,
		&auth.Users{
			UserId:    userId,
			Username:  req.RegisterInfo.UserName,
			Password:  string(hashedPassword),
			Email:     req.RegisterInfo.Email,
		CreatedAt: common.Now(),
		UpdatedAt: common.Now(),
		},
	)
	if err != nil {
		l.Logger.Errorf("Failed to insert user: %v", err)
		return &types.RegisterResp{
			BaseResp: types.BaseResp{
				Code:    500,
				Message: MsgInsertUserFailed,
			},
		}, err
	}

	lastInsertID, err := result.LastInsertId()
	if err != nil {
		l.Logger.Errorf("Failed to get last insert ID: %v", err)
		return &types.RegisterResp{
			BaseResp: types.BaseResp{
				Code:    500,
				Message: MsgGetUserIdFailed,
			},
		}, err
	}
	l.Logger.Infof("Inserted user with ID: %d", lastInsertID)

	// 为新注册的用户分配默认角色
	defaultRoleName := "user"
	role, err := l.svcCtx.CustomCtx.SQLModel.RolesModel.FindOneByName(l.ctx, defaultRoleName)
	if err != nil {
		l.Logger.Errorf("Failed to find default role '%s': %v", defaultRoleName, err)
		return &types.RegisterResp{
			BaseResp: types.BaseResp{
				Code:    500,
				Message: MsgFindDefaultRoleFailed,
			},
		}, nil
	}

	_, err = l.svcCtx.CustomCtx.SQLModel.UserRolesModel.Insert(l.ctx, &rbac.UserRoles{
		UserId: userId,
		RoleId: role.Id,
	})
	if err != nil {
		l.Logger.Errorf("Failed to assign default role to user '%s': %v", userId, err)
		return &types.RegisterResp{
			BaseResp: types.BaseResp{
				Code:    500,
				Message: MsgAssignDefaultRoleFail,
			},
		}, nil
	}

	return &types.RegisterResp{
		BaseResp: types.BaseResp{
			Code:    200,
			Message: MsgRegisterSuccess,
		},
		RegisterRespBody: types.RegisterRespBody{
			RegisteredInfo: types.RegisteredInfo{
				UserId:   userId,
				UserName: req.RegisterInfo.UserName,
				Email:    req.RegisterInfo.Email,
			},
		},
	}, nil
}
