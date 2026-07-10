// Code scaffolded by goctl. Safe to edit.
// goctl 1.10.1

package markdown

import (
	"context"

	"app/internal/common"
	"app/internal/svc"
	"app/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type GetReviewInfoLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewGetReviewInfoLogic(ctx context.Context, svcCtx *svc.ServiceContext) *GetReviewInfoLogic {
	return &GetReviewInfoLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *GetReviewInfoLogic) GetReviewInfo(req *types.GetReviewInfoReq) (resp *types.GetReviewInfoResp, err error) {
	if err := common.RequirePermission(l.ctx, l.svcCtx.CustomCtx.SQLModel, "markdown", "review"); err != nil {
		return nil, err
	}

	review, err := l.svcCtx.CustomCtx.SQLModel.MarkdownReviewInfoModel.FindOneByMarkdownId(l.ctx, req.MarkdownId)
	if err != nil {
		l.Logger.Errorf("review info not found: markdownId=%s, err=%v", req.MarkdownId, err)
		return &types.GetReviewInfoResp{BaseResp: types.BaseResp{Code: 404, Message: MsgReviewInfoNotFound}}, nil
	}

	return &types.GetReviewInfoResp{
		BaseResp: types.BaseResp{Code: 200, Message: MsgSuccess},
		GetReviewInfoRespBody: types.GetReviewInfoRespBody{
			MarkdownId:    review.MarkdownId,
			ReviewerId:    review.ReviewerId,
			ReviewStatus:  review.ReviewStatus,
			ReviewComment: review.ReviewComment,
			ReviewAt:      review.ReviewAt.String(),
		},
	}, nil
}
