// Code scaffolded by goctl. Safe to edit.
// goctl 1.10.1

package markdown

import (
	"context"

	"app/internal/common"
	"app/internal/logic/markdown/utils"
	"app/internal/model/markdown"
	"app/internal/svc"
	"app/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type UpdateMarkdownLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewUpdateMarkdownLogic(ctx context.Context, svcCtx *svc.ServiceContext) *UpdateMarkdownLogic {
	return &UpdateMarkdownLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *UpdateMarkdownLogic) UpdateMarkdown(req *types.UpdateMarkdownReq) (resp *types.UpdateMarkdownResp, err error) {
	if err := common.RequirePermission(l.ctx, l.svcCtx.CustomCtx.SQLModel, "markdown", "update"); err != nil {
		return nil, err
	}

	existing, err := l.svcCtx.CustomCtx.SQLModel.MarkdownModel.FindOneByMarkdownId(l.ctx, req.MarkdownId)
	if err != nil {
		l.Logger.Errorf("markdown not found: id=%s, err=%v", req.MarkdownId, err)
		return &types.UpdateMarkdownResp{BaseResp: types.BaseResp{Code: 404, Message: MsgNotFound}}, nil
	}

	if err := utils.RequireAuthor(l.ctx, existing.AuthorId); err != nil {
		return &types.UpdateMarkdownResp{BaseResp: types.BaseResp{Code: 403, Message: MsgNotAuthor}}, nil
	}

	now := common.Now()
	// 更新主表
	if err = l.svcCtx.CustomCtx.SQLModel.MarkdownModel.Update(l.ctx, &markdown.Markdown{
		Id:         existing.Id,
		MarkdownId: existing.MarkdownId,
		AuthorId:   existing.AuthorId,
		Title:      req.Title,
		Summary:    utils.TruncateSummary(req.Content),
		CreatedAt:  existing.CreatedAt,
		UpdatedAt:  now,
		DeletedAt:  existing.DeletedAt,
	}); err != nil {
		l.Logger.Errorf("update markdown failed: %v", err)
		return &types.UpdateMarkdownResp{BaseResp: types.BaseResp{Code: 500, Message: MsgUpdateFailed}}, err
	}

	// 更新正文表
	if content, err := l.svcCtx.CustomCtx.SQLModel.MarkdownContentModel.FindOneByMarkdownId(l.ctx, req.MarkdownId); err == nil {
		content.Content = req.Content
		if err := l.svcCtx.CustomCtx.SQLModel.MarkdownContentModel.Update(l.ctx, content); err != nil {
			l.Logger.Errorf("update markdown content failed: %v", err)
		}
	}

	// 重置审核状态
	if review, err := l.svcCtx.CustomCtx.SQLModel.MarkdownReviewInfoModel.FindOneByMarkdownId(l.ctx, req.MarkdownId); err == nil {
		review.ReviewStatus = "pending"
		review.ReviewComment = ""
		review.ReviewAt = now
		if err := l.svcCtx.CustomCtx.SQLModel.MarkdownReviewInfoModel.Update(l.ctx, review); err != nil {
			l.Logger.Errorf("reset review status failed: %v", err)
		}
	}

	m, ct, rv, err := utils.LoadFull(l.ctx, l.svcCtx.CustomCtx.SQLModel, req.MarkdownId)
	if err != nil {
		return &types.UpdateMarkdownResp{BaseResp: types.BaseResp{Code: 500, Message: MsgUpdateFailed}}, err
	}

	// 列表缓存失效
	// TODO: 缓存失效逻辑

	return &types.UpdateMarkdownResp{
		BaseResp: types.BaseResp{Code: 200, Message: MsgSuccess},
		UpdateMarkdownRespBody: types.UpdateMarkdownRespBody{
			Markdown: utils.ToApiMarkdown(m, ct, rv),
		},
	}, nil
}
