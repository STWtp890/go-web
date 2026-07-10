// Code scaffolded by goctl. Safe to edit.
// goctl 1.10.1

package announcement

import (
	"context"
	"database/sql"
	"time"

	"app/internal/common"
	"app/internal/model/announcement"
	"app/internal/svc"
	"app/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type CreateAnnouncementLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewCreateAnnouncementLogic(ctx context.Context, svcCtx *svc.ServiceContext) *CreateAnnouncementLogic {
	return &CreateAnnouncementLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *CreateAnnouncementLogic) CreateAnnouncement(req *types.CreateAnnouncementReq) (resp *types.CreateAnnouncementResp, err error) {
	if err := common.RequirePermission(l.ctx, l.svcCtx.CustomCtx.SQLModel, "announcement", "create"); err != nil {
		return nil, err
	}

	announcementId := common.MustUUIDv7()
	now := common.Now()

	// 获取发布人
	userId, err := common.GetUserIdFromCtx(l.ctx)
	if err != nil {
		return &types.CreateAnnouncementResp{BaseResp: types.BaseResp{Code: 401, Message: MsgUnauthorized}}, nil
	}

	// 1. 写入主表
	if _, err = l.svcCtx.CustomCtx.SQLModel.AnnouncementModel.Insert(l.ctx, &announcement.Announcement{
		AnnouceUserId:  userId,
		AnnouncementId: announcementId,
		Title:          req.Title,
		CreatedAt:      now,
		UpdatedAt:      now,
		DeletedAt:      sql.NullTime{},
	}); err != nil {
		l.Logger.Errorf("insert announcement failed: %v", err)
		return &types.CreateAnnouncementResp{BaseResp: types.BaseResp{Code: 500, Message: MsgCreateFailed}}, err
	}

	// 2. 写入正文表
	if _, err = l.svcCtx.CustomCtx.SQLModel.AnnouncementContentModel.Insert(l.ctx, &announcement.AnnouncementContent{
		AnnouncementId: announcementId,
		Content:        req.Content,
	}); err != nil {
		l.Logger.Errorf("insert announcement content failed: %v", err)
		return &types.CreateAnnouncementResp{BaseResp: types.BaseResp{Code: 500, Message: MsgCreateFailed}}, err
	}

	return &types.CreateAnnouncementResp{
		BaseResp: types.BaseResp{Code: 200, Message: MsgSuccess},
		CreateAnnouncementRespBody: types.CreateAnnouncementRespBody{
			Announcement: types.AnnouncementDetail{
				AnnouncementId: announcementId,
				AnnouceUserId:  userId,
				Title:          req.Title,
				Content:        req.Content,
				CreatedAt:      now.Format(time.RFC3339),
				UpdatedAt:      now.Format(time.RFC3339),
			},
		},
	}, nil
}
