// Code scaffolded by goctl. Safe to edit.
// goctl 1.10.1

package markdown

import (
	"context"

	"app/internal/logic/markdown/utils"
	"app/internal/svc"
	"app/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type PreviewMarkdownLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewPreviewMarkdownLogic(ctx context.Context, svcCtx *svc.ServiceContext) *PreviewMarkdownLogic {
	return &PreviewMarkdownLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *PreviewMarkdownLogic) PreviewMarkdown(req *types.PreviewMarkdownReq) (resp *types.PreviewMarkdownResp, err error) {
	m, content, review, err := utils.LoadFull(l.ctx, l.svcCtx.CustomCtx.SQLModel, req.MarkdownId)
	if err != nil {
		l.Logger.Errorf("markdown not found: id=%s, err=%v", req.MarkdownId, err)
		return &types.PreviewMarkdownResp{BaseResp: types.BaseResp{Code: 404, Message: MsgNotFound}}, nil
	}

	return &types.PreviewMarkdownResp{
		BaseResp: types.BaseResp{Code: 200, Message: MsgSuccess},
		PreviewMarkdownRespBody: types.PreviewMarkdownRespBody{
			Markdown: utils.ToApiMarkdown(m, content, review),
		},
	}, nil
}
