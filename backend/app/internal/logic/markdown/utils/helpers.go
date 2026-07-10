package utils

import (
	"context"
	"time"

	"app/internal/common"
	"app/internal/model/markdown"
	"app/internal/svc"
	"app/internal/types"
)

// ToApiMarkdown 将分表数据组合为 Detail 类型（含 Content，用于 preview/upload/update 响应）
func ToApiMarkdown(m markdown.Markdown, content markdown.MarkdownContent, review markdown.MarkdownReviewInfo) types.MarkdownDetail {
	return types.MarkdownDetail{
		MarkdownId:    m.MarkdownId,
		AuthorId:      m.AuthorId,
		Title:         m.Title,
		Summary:       m.Summary,
		Content:       content.Content,
		ReviewerId:    review.ReviewerId,
		ReviewStatus:  review.ReviewStatus,
		ReviewComment: review.ReviewComment,
		CreatedAt:     m.CreatedAt.Format(time.RFC3339),
		UpdatedAt:     m.UpdatedAt.Format(time.RFC3339),
	}
}

// LoadFull 加载完整 markdown 数据（metadata + content + review）
func LoadFull(ctx context.Context, m *svc.SQLModel, markdownId string) (markdown.Markdown, markdown.MarkdownContent, markdown.MarkdownReviewInfo, error) {
	md, err := m.MarkdownModel.FindOneByMarkdownId(ctx, markdownId)
	if err != nil {
		return markdown.Markdown{}, markdown.MarkdownContent{}, markdown.MarkdownReviewInfo{}, err
	}
	content, err := m.MarkdownContentModel.FindOneByMarkdownId(ctx, markdownId)
	if err != nil {
		return *md, markdown.MarkdownContent{}, markdown.MarkdownReviewInfo{}, err
	}
	review, err := m.MarkdownReviewInfoModel.FindOneByMarkdownId(ctx, markdownId)
	if err != nil {
		return *md, *content, markdown.MarkdownReviewInfo{}, err
	}
	return *md, *content, *review, nil
}

// RequireAuthor 校验当前请求用户是否为指定作品的作者。
func RequireAuthor(ctx context.Context, authorId string) error {
	roleId, err := common.GetRoleIdFromCtx(ctx)
	if err != nil {
		return err
	}
	if roleId == common.SuperAdminRoleId {
		return nil
	}

	userId, err := common.GetUserIdFromCtx(ctx)
	if err != nil {
		return err
	}
	if userId != authorId {
		return common.NewBizError(403, "forbidden: not the author")
	}
	return nil
}
