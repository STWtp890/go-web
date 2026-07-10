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

type UploadMarkdownLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewUploadMarkdownLogic(ctx context.Context, svcCtx *svc.ServiceContext) *UploadMarkdownLogic {
	return &UploadMarkdownLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *UploadMarkdownLogic) UploadMarkdown(req *types.UploadMarkdownReq) (resp *types.UploadMarkdownResp, err error) {
	if err := common.RequirePermission(l.ctx, l.svcCtx.CustomCtx.SQLModel, "markdown", "create"); err != nil {
		return nil, err
	}

	authorId, err := common.GetUserIdFromCtx(l.ctx)
	if err != nil {
		return &types.UploadMarkdownResp{BaseResp: types.BaseResp{Code: 401, Message: MsgMissingAuthorId}}, nil
	}

	markdownId := common.MustUUIDv7()
	summary := utils.TruncateSummary(req.Content)
	now := common.Now()

	// 1. 写入主表
	if _, err = l.svcCtx.CustomCtx.SQLModel.MarkdownModel.Insert(l.ctx, &markdown.Markdown{
		MarkdownId: markdownId,
		AuthorId:   authorId,
		Title:      req.Title,
		Summary:    summary,
		CreatedAt:  now,
		UpdatedAt:  now,
	}); err != nil {
		l.Logger.Errorf("insert markdown failed: %v", err)
		return &types.UploadMarkdownResp{BaseResp: types.BaseResp{Code: 500, Message: MsgUploadFailed}}, err
	}

	// 2. 写入正文表
	if _, err = l.svcCtx.CustomCtx.SQLModel.MarkdownContentModel.Insert(l.ctx, &markdown.MarkdownContent{
		MarkdownId: markdownId,
		Content:    req.Content,
	}); err != nil {
		l.Logger.Errorf("insert markdown content failed: %v", err)
		return &types.UploadMarkdownResp{BaseResp: types.BaseResp{Code: 500, Message: MsgUploadFailed}}, err
	}

	// 3. 写入审核表（初始状态 pending）
	if _, err = l.svcCtx.CustomCtx.SQLModel.MarkdownReviewInfoModel.Insert(l.ctx, &markdown.MarkdownReviewInfo{
		MarkdownId:    markdownId,
		ReviewStatus:  "pending",
		ReviewComment: "",
		ReviewAt:      now,
	}); err != nil {
		l.Logger.Errorf("insert review info failed: %v", err)
		return &types.UploadMarkdownResp{BaseResp: types.BaseResp{Code: 500, Message: MsgUploadFailed}}, err
	}

	// 返回完整数据
	m, content, review, err := utils.LoadFull(l.ctx, l.svcCtx.CustomCtx.SQLModel, markdownId)
	if err != nil {
		return &types.UploadMarkdownResp{BaseResp: types.BaseResp{Code: 500, Message: MsgUploadFailed}}, err
	}

	return &types.UploadMarkdownResp{
		BaseResp: types.BaseResp{Code: 200, Message: MsgSuccess},
		UploadMarkdownRespBody: types.UploadMarkdownRespBody{
			Markdown: utils.ToApiMarkdown(m, content, review),
		},
	}, nil
}
