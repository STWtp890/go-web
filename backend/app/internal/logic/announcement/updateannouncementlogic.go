// Code scaffolded by goctl. Safe to edit.
// goctl 1.10.1

package announcement

import (
	"context"
	"time"

	"app/internal/common"
	"app/internal/model/announcement"
	"app/internal/svc"
	"app/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type UpdateAnnouncementLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewUpdateAnnouncementLogic(ctx context.Context, svcCtx *svc.ServiceContext) *UpdateAnnouncementLogic {
	return &UpdateAnnouncementLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *UpdateAnnouncementLogic) UpdateAnnouncement(req *types.UpdateAnnouncementReq) (resp *types.UpdateAnnouncementResp, err error) {
	if err := common.RequirePermission(l.ctx, l.svcCtx.CustomCtx.SQLModel, "announcement", "update"); err != nil {
		return nil, err
	}

	existing, err := l.svcCtx.CustomCtx.SQLModel.AnnouncementModel.FindOneByAnnouncementId(l.ctx, req.AnnouncementId)
	if err != nil {
		l.Logger.Errorf("announcement not found: id=%s, err=%v", req.AnnouncementId, err)
		return &types.UpdateAnnouncementResp{BaseResp: types.BaseResp{Code: 404, Message: MsgNotFound}}, nil
	}

	now := common.Now()
	// 更新主表
	if err = l.svcCtx.CustomCtx.SQLModel.AnnouncementModel.Update(l.ctx, &announcement.Announcement{
		Id:             existing.Id,
		AnnouncementId: existing.AnnouncementId,
		AnnouceUserId:  existing.AnnouceUserId,
		Title:          req.Title,
		CreatedAt:      existing.CreatedAt,
		UpdatedAt:      now,
		DeletedAt:      existing.DeletedAt,
	}); err != nil {
		l.Logger.Errorf("update announcement failed: %v", err)
		return &types.UpdateAnnouncementResp{BaseResp: types.BaseResp{Code: 500, Message: MsgUpdateFailed}}, err
	}

	// 更新正文表
	if content, err := l.svcCtx.CustomCtx.SQLModel.AnnouncementContentModel.FindOneByAnnouncementId(l.ctx, req.AnnouncementId); err == nil {
		content.Content = req.Content
		if err := l.svcCtx.CustomCtx.SQLModel.AnnouncementContentModel.Update(l.ctx, content); err != nil {
			l.Logger.Errorf("update announcement content failed: %v", err)
		}
	}

	return &types.UpdateAnnouncementResp{
		BaseResp: types.BaseResp{Code: 200, Message: MsgSuccess},
		UpdateAnnouncementRespBody: types.UpdateAnnouncementRespBody{
			Announcement: types.AnnouncementDetail{
				AnnouncementId: existing.AnnouncementId,
				AnnouceUserId:  existing.AnnouceUserId,
				Title:          req.Title,
				Content:        req.Content,
				CreatedAt:      existing.CreatedAt.Format(time.RFC3339),
				UpdatedAt:      now.Format(time.RFC3339),
			},
		},
	}, nil
}
