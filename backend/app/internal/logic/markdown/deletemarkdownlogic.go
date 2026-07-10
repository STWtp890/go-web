// Code scaffolded by goctl. Safe to edit.
// goctl 1.10.1

package markdown

import (
	"context"

	"app/internal/common"
	"app/internal/logic/markdown/utils"
	"app/internal/svc"
	"app/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type DeleteMarkdownLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewDeleteMarkdownLogic(ctx context.Context, svcCtx *svc.ServiceContext) *DeleteMarkdownLogic {
	return &DeleteMarkdownLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *DeleteMarkdownLogic) DeleteMarkdown(req *types.DeleteMarkdownReq) (resp *types.DeleteMarkdownResp, err error) {
	if err := common.RequirePermission(l.ctx, l.svcCtx.CustomCtx.SQLModel, "markdown", "delete"); err != nil {
		return nil, err
	}

	m, err := l.svcCtx.CustomCtx.SQLModel.MarkdownModel.FindOneByMarkdownId(l.ctx, req.MarkdownId)
	if err != nil {
		l.Logger.Errorf("markdown not found: id=%s, err=%v", req.MarkdownId, err)
		return &types.DeleteMarkdownResp{BaseResp: types.BaseResp{Code: 404, Message: MsgNotFound}}, nil
	}

	if err := utils.RequireAuthor(l.ctx, m.AuthorId); err != nil {
		return &types.DeleteMarkdownResp{BaseResp: types.BaseResp{Code: 403, Message: MsgNotAuthor}}, nil
	}

	err = common.SoftDelete(l.ctx, *l.svcCtx.CustomCtx.SqlxConn, "`markdown`", m.Id)
	if err != nil {
		l.Logger.Errorf("soft delete markdown %s failed: %v", req.MarkdownId, err)
		return &types.DeleteMarkdownResp{BaseResp: types.BaseResp{Code: 500, Message: MsgDeleteFailed}}, err
	}

	return &types.DeleteMarkdownResp{
		BaseResp: types.BaseResp{Code: 200, Message: MsgSuccess},
	}, nil
}
