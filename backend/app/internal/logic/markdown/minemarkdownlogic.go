package markdown

import (
	"context"
	"strings"
	"time"

	"app/internal/common"
	"app/internal/model/markdown"
	"app/internal/svc"
	"app/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type MineMarkdownLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewMineMarkdownLogic(ctx context.Context, svcCtx *svc.ServiceContext) *MineMarkdownLogic {
	return &MineMarkdownLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

func (l *MineMarkdownLogic) MineMarkdown(req *types.MineMarkdownReq) (resp *types.ListMarkdownResp, err error) {
	type row struct {
		markdown.Markdown
		ReviewerId    string `db:"reviewer_id"`
		ReviewStatus  string `db:"review_status"`
		ReviewComment string `db:"review_comment"`
	}

	// 构建动态过滤
	var conditions []string
	var args []any

	conditions = append(conditions, "m.deleted_at IS NULL")
	conditions = append(conditions, "m.author_id = ?")
	args = append(args, req.UserId)

	if req.ReviewStatus != "" {
		conditions = append(conditions, "r.review_status = ?")
		args = append(args, req.ReviewStatus)
	}

	where := strings.Join(conditions, " AND ")

	pageInfo, dbList, err := common.PaginateQuery[row](
		l.ctx, *l.svcCtx.CustomCtx.SqlxConn,
		"SELECT COUNT(*) FROM markdown m INNER JOIN markdown_review_info r ON r.markdown_id = m.markdown_id WHERE "+where, args,
		"SELECT m.*, r.reviewer_id, r.review_status, r.review_comment FROM markdown m INNER JOIN markdown_review_info r ON r.markdown_id = m.markdown_id WHERE "+where+" ORDER BY m.created_at DESC", args,
		req.PageNumber, req.PageSize,
	)
	if err != nil {
		l.Logger.Errorf("mine markdown list failed: %v", err)
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
