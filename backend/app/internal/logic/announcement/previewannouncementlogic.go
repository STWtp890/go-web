package announcement

import (
	"context"
	"time"

	"app/internal/svc"
	"app/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type PreviewAnnouncementLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewPreviewAnnouncementLogic(ctx context.Context, svcCtx *svc.ServiceContext) *PreviewAnnouncementLogic {
	return &PreviewAnnouncementLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *PreviewAnnouncementLogic) PreviewAnnouncement(req *types.PreviewAnnouncementReq) (resp *types.PreviewAnnouncementResp, err error) {
	ann, err := l.svcCtx.CustomCtx.SQLModel.AnnouncementModel.FindOneByAnnouncementId(l.ctx, req.AnnouncementId)
	if err != nil {
		l.Logger.Errorf("announcement not found: id=%s, err=%v", req.AnnouncementId, err)
		return &types.PreviewAnnouncementResp{BaseResp: types.BaseResp{Code: 404, Message: MsgNotFound}}, nil
	}

	contentStr := ""
	if c, err := l.svcCtx.CustomCtx.SQLModel.AnnouncementContentModel.FindOneByAnnouncementId(l.ctx, req.AnnouncementId); err == nil {
		contentStr = c.Content
	}

	return &types.PreviewAnnouncementResp{
		BaseResp: types.BaseResp{Code: 200, Message: MsgSuccess},
		PreviewAnnouncementRespBody: types.PreviewAnnouncementRespBody{
			Announcement: types.AnnouncementDetail{
				AnnouncementId: ann.AnnouncementId,
				AnnouceUserId:  ann.AnnouceUserId,
				Title:          ann.Title,
				Content:        contentStr,
				CreatedAt:      ann.CreatedAt.Format(time.RFC3339),
				UpdatedAt:      ann.UpdatedAt.Format(time.RFC3339),
			},
		},
	}, nil
}
