// Code scaffolded by goctl. Safe to edit.
// goctl 1.10.1

package markdown

import (
	"context"
	"time"

	"app/internal/common"
	"app/internal/model/markdown"
	"app/internal/svc"
	"app/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type ListUnreviewedMarkdownsLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewListUnreviewedMarkdownsLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ListUnreviewedMarkdownsLogic {
	return &ListUnreviewedMarkdownsLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *ListUnreviewedMarkdownsLogic) ListUnreviewedMarkdowns(req *types.ListMarkdownReq) (resp *types.ListMarkdownResp, err error) {
	if err := common.RequirePermission(l.ctx, l.svcCtx.CustomCtx.SQLModel, "markdown", "review"); err != nil {
		return nil, err
	}

	type row struct {
		markdown.Markdown
		ReviewerId    string `db:"reviewer_id"`
		ReviewStatus  string `db:"review_status"`
		ReviewComment string `db:"review_comment"`
	}
	pageInfo, dbList, err := common.PaginateQuery[row](
		l.ctx, *l.svcCtx.CustomCtx.SqlxConn,
		"SELECT COUNT(*) FROM markdown m INNER JOIN markdown_review_info r ON r.markdown_id = m.markdown_id WHERE m.deleted_at IS NULL AND r.review_status = 'pending'", nil,
		"SELECT m.*, r.reviewer_id, r.review_status, r.review_comment FROM markdown m INNER JOIN markdown_review_info r ON r.markdown_id = m.markdown_id WHERE m.deleted_at IS NULL AND r.review_status = 'pending' ORDER BY m.created_at ASC", nil,
		req.PageNumber, req.PageSize,
	)
	if err != nil {
		l.Logger.Errorf("list unreviewed markdowns failed: %v", err)
		return &types.ListMarkdownResp{BaseResp: types.BaseResp{Code: 500, Message: MsgQueryFailed}}, err
	}

	list := make([]types.MarkdownListItem, 0, len(dbList))
	for _, r := range dbList {
		list = append(list, types.MarkdownListItem{
			MarkdownId:    r.MarkdownId,
			AuthorId:      r.AuthorId,
			Title:         r.Title,
			Summary:       r.Summary,
			ReviewerId:    r.ReviewerId,
			ReviewStatus:  r.ReviewStatus,
			ReviewComment: r.ReviewComment,
			CreatedAt:     r.CreatedAt.Format(time.RFC3339),
			UpdatedAt:     r.UpdatedAt.Format(time.RFC3339),
		})
	}

	return &types.ListMarkdownResp{
		BaseResp: types.BaseResp{Code: 200, Message: MsgSuccess},
		PageInfo: *pageInfo,
		ListMarkdownRespBody: types.ListMarkdownRespBody{
			MarkdownList: types.MarkdownListItemList{MarkdownList: list},
		},
	}, nil
}
