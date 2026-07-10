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

type ListReviewsLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewListReviewsLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ListReviewsLogic {
	return &ListReviewsLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *ListReviewsLogic) ListReviews(req *types.ListReviewReq) (resp *types.ListReviewResp, err error) {
	if err := common.RequirePermission(l.ctx, l.svcCtx.CustomCtx.SQLModel, "markdown", "review:list"); err != nil {
		return nil, err
	}

	countWhere := "m.deleted_at IS NULL"
	dataWhere := "m.deleted_at IS NULL"
	var args []any
	if req.Status != "" {
		countWhere += " AND r.review_status = ?"
		dataWhere += " AND r.review_status = ?"
		args = append(args, req.Status)
	}

	type row struct {
		markdown.Markdown
		ReviewerId    string `db:"reviewer_id"`
		ReviewStatus  string `db:"review_status"`
		ReviewComment string `db:"review_comment"`
	}
	pageInfo, dbList, err := common.PaginateQuery[row](
		l.ctx, *l.svcCtx.CustomCtx.SqlxConn,
		"SELECT COUNT(*) FROM markdown m INNER JOIN markdown_review_info r ON r.markdown_id = m.markdown_id WHERE "+countWhere, args,
		"SELECT m.*, r.reviewer_id, r.review_status, r.review_comment FROM markdown m INNER JOIN markdown_review_info r ON r.markdown_id = m.markdown_id WHERE "+dataWhere+" ORDER BY m.created_at DESC", args,
		req.PageNumber, req.PageSize,
	)
	if err != nil {
		l.Logger.Errorf("list reviews failed: %v", err)
		return &types.ListReviewResp{BaseResp: types.BaseResp{Code: 500, Message: MsgQueryFailed}}, err
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

	return &types.ListReviewResp{
		BaseResp: types.BaseResp{Code: 200, Message: MsgSuccess},
		PageInfo: *pageInfo,
		ListReviewRespBody: types.ListReviewRespBody{
			MarkdownList: types.MarkdownListItemList{MarkdownList: list},
		},
	}, nil
}
