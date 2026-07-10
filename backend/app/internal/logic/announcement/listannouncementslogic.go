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

type ListAnnouncementsLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewListAnnouncementsLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ListAnnouncementsLogic {
	return &ListAnnouncementsLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *ListAnnouncementsLogic) ListAnnouncements(req *types.ListAnnouncementReq) (resp *types.ListAnnouncementResp, err error) {
	// 列表仅查主表，不含正文（detail 用 preview 端点）
	pageInfo, dbList, err := common.PaginateQuery[announcement.Announcement](
		l.ctx, *l.svcCtx.CustomCtx.SqlxConn,
		"SELECT COUNT(*) FROM announcement WHERE deleted_at IS NULL", nil,
		"SELECT id, annouce_user_id, announcement_id, title, created_at, updated_at, deleted_at FROM announcement WHERE deleted_at IS NULL ORDER BY created_at DESC", nil,
		req.PageNumber, req.PageSize,
	)
	if err != nil {
		l.Logger.Errorf("list announcements failed: %v", err)
		return &types.ListAnnouncementResp{BaseResp: types.BaseResp{Code: 500, Message: MsgQueryFailed}}, err
	}

	list := make([]types.AnnouncementListItem, 0, len(dbList))
	for _, a := range dbList {
		list = append(list, types.AnnouncementListItem{
			AnnouncementId: a.AnnouncementId,
			AnnouceUserId:  a.AnnouceUserId,
			Title:          a.Title,
			CreatedAt:      a.CreatedAt.Format(time.RFC3339),
			UpdatedAt:      a.UpdatedAt.Format(time.RFC3339),
		})
	}

	return &types.ListAnnouncementResp{
		BaseResp: types.BaseResp{Code: 200, Message: MsgSuccess},
		PageInfo: *pageInfo,
		ListAnnouncementRespBody: types.ListAnnouncementRespBody{
			AnnouncementList: types.AnnouncementListItemList{AnnouncementList: list},
		},
	}, nil
}
