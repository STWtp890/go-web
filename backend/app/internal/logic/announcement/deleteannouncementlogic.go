// Code scaffolded by goctl. Safe to edit.
// goctl 1.10.1

package announcement

import (
	"context"

	"app/internal/common"
	"app/internal/svc"
	"app/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type DeleteAnnouncementLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewDeleteAnnouncementLogic(ctx context.Context, svcCtx *svc.ServiceContext) *DeleteAnnouncementLogic {
	return &DeleteAnnouncementLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *DeleteAnnouncementLogic) DeleteAnnouncement(req *types.DeleteAnnouncementReq) (resp *types.DeleteAnnouncementResp, err error) {
	if err := common.RequirePermission(l.ctx, l.svcCtx.CustomCtx.SQLModel, "announcement", "delete"); err != nil {
		return nil, err
	}

	// 通过对象ID (UUIDv7) 查找内部 id
	ann, err := l.svcCtx.CustomCtx.SQLModel.AnnouncementModel.FindOneByAnnouncementId(l.ctx, req.AnnouncementId)
	if err != nil {
		l.Logger.Errorf("announcement not found: id=%s, err=%v", req.AnnouncementId, err)
		return &types.DeleteAnnouncementResp{
			BaseResp: types.BaseResp{Code: 404, Message: MsgNotFound},
		}, nil
	}

	if err = common.SoftDelete(l.ctx, *l.svcCtx.CustomCtx.SqlxConn, "`announcement`", ann.Id); err != nil {
		l.Logger.Errorf("soft delete announcement %s failed: %v", req.AnnouncementId, err)
		return &types.DeleteAnnouncementResp{
			BaseResp: types.BaseResp{Code: 500, Message: MsgDeleteFailed},
		}, err
	}

	return &types.DeleteAnnouncementResp{
		BaseResp: types.BaseResp{Code: 200, Message: MsgSuccess},
	}, nil
}
