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

type ReviewMarkdownLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewReviewMarkdownLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ReviewMarkdownLogic {
	return &ReviewMarkdownLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *ReviewMarkdownLogic) ReviewMarkdown(req *types.ReviewMarkdownReq) (resp *types.ReviewMarkdownResp, err error) {
	if err := common.RequirePermission(l.ctx, l.svcCtx.CustomCtx.SQLModel, "markdown", "review"); err != nil {
		return nil, err
	}

	// 校验 status 值
	if req.Status != "approved" && req.Status != "rejected" {
		return &types.ReviewMarkdownResp{
			BaseResp: types.BaseResp{Code: 400, Message: MsgInvalidStatus},
		}, nil
	}

	// 通过对象ID (UUIDv7) 查找 markdown 确认存在
	review, err := l.svcCtx.CustomCtx.SQLModel.MarkdownReviewInfoModel.FindOneByMarkdownId(l.ctx, req.MarkdownId)
	if err != nil {
		l.Logger.Errorf("review info not found: markdownId=%s, err=%v", req.MarkdownId, err)
		return &types.ReviewMarkdownResp{BaseResp: types.BaseResp{Code: 404, Message: MsgReviewInfoNotFound}}, nil
	}

	reviewerId, err := common.GetUserIdFromCtx(l.ctx)
	if err != nil || reviewerId == "" {
		l.Logger.Errorf("failed to get reviewerId from context: %v", err)
		return &types.ReviewMarkdownResp{BaseResp: types.BaseResp{Code: 401, Message: MsgMissingReviewerId}}, nil
	}

	now := common.Now()
	review.ReviewerId = reviewerId
	review.ReviewStatus = req.Status
	review.ReviewComment = req.Comment
	review.ReviewAt = now
	if err = l.svcCtx.CustomCtx.SQLModel.MarkdownReviewInfoModel.Update(l.ctx, review); err != nil {
		l.Logger.Errorf("update review info failed: markdownId=%s, err=%v", req.MarkdownId, err)
		return &types.ReviewMarkdownResp{BaseResp: types.BaseResp{Code: 500, Message: MsgReviewFailed}}, err
	}

	// 缓存刷新：删除 markdown 缓存，确保下次访问时获取最新数据

	// 获取 authorId 用于响应
	md, err := l.svcCtx.CustomCtx.SQLModel.MarkdownModel.FindOneByMarkdownId(l.ctx, req.MarkdownId)
	if err != nil {
		l.Logger.Errorf("failed to find markdown: markdownId=%s, err=%v", req.MarkdownId, err)
		return &types.ReviewMarkdownResp{BaseResp: types.BaseResp{Code: 500, Message: MsgFindMarkdownFailed}}, err
	}
	authorId := ""
	if md != nil {
		authorId = md.AuthorId
	}

	return &types.ReviewMarkdownResp{
		BaseResp: types.BaseResp{Code: 200, Message: MsgSuccess},
		ReviewMarkdownRespBody: types.ReviewMarkdownRespBody{
			MarkdownId: req.MarkdownId,
			AuthorId:   authorId,
			Status:     req.Status,
			Comment:    req.Comment,
		},
	}, nil
}
