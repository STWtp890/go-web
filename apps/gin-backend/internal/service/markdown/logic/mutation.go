package logic

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gin-backend/internal/common/base/connection/postgresql"
	markdownmodel "gin-backend/internal/model/orm/markdown"
	"gin-backend/internal/model/store"
	"gin-backend/internal/service/markdown/types/requests"
	"gin-backend/internal/service/markdown/utils"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// UpdateMarkdownLogic 全量更新作者自己的文章。
// 元信息、正文和全文检索字段在同一事务内更新，提交后统一失效缓存。
func UpdateMarkdownLogic(ctx context.Context, authorID, markdownID string, req *requests.UpdateMarkdownRequest) (*markdownmodel.Markdown, error) {
	if err := validateMutationInput(authorID, markdownID, req); err != nil {
		return nil, err
	}

	title := strings.TrimSpace(req.Title)
	summary := utils.BuildSummary(req.Content)
	searchText := title + " " + summary + " " + req.Content
	db, err := utils.MarkdownDB(ctx)
	if err != nil {
		return nil, err
	}

	var updated markdownmodel.Markdown
	err = postgresql.Transaction(db, func(tx *gorm.DB) error {
		var md markdownmodel.Markdown
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("markdown_id = ?", markdownID).
			First(&md).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrMarkdownNotFound
			}
			return err
		}
		if md.AuthorID != authorID {
			return ErrMarkdownForbidden
		}

		now := time.Now().Unix()
		if err := tx.Model(&md).Updates(map[string]any{
			"title":       title,
			"summary":     summary,
			"visibility":  req.Visibility,
			"search_text": searchText,
			"updated_at":  now,
		}).Error; err != nil {
			return err
		}
		result := tx.Model(&markdownmodel.Content{}).
			Where("markdown_id = ?", markdownID).
			Update("content", req.Content)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("markdown: 正文记录不存在")
		}

		md.Title = title
		md.Summary = summary
		md.Visibility = req.Visibility
		md.SearchText = searchText
		md.UpdatedAt = now
		updated = md
		return nil
	})
	if err != nil {
		return nil, err
	}

	_ = store.Markdown.Evict(ctx, markdownID)
	return &updated, nil
}

// DeleteMarkdownLogic 删除作者自己的文章。
// 元信息使用 GORM 软删除；无软删除字段的正文记录在同一事务内物理删除。
func DeleteMarkdownLogic(ctx context.Context, authorID, markdownID string) error {
	if strings.TrimSpace(authorID) == "" || strings.TrimSpace(markdownID) == "" {
		return ErrMarkdownInvalidInput
	}
	db, err := utils.MarkdownDB(ctx)
	if err != nil {
		return err
	}

	err = postgresql.Transaction(db, func(tx *gorm.DB) error {
		var md markdownmodel.Markdown
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("markdown_id = ?", markdownID).
			First(&md).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrMarkdownNotFound
			}
			return err
		}
		if md.AuthorID != authorID {
			return ErrMarkdownForbidden
		}

		if err := tx.Where("markdown_id = ?", markdownID).
			Delete(&markdownmodel.Content{}).Error; err != nil {
			return err
		}
		return tx.Delete(&md).Error
	})
	if err != nil {
		return err
	}

	_ = store.Markdown.Evict(ctx, markdownID)
	return nil
}

func validateMutationInput(authorID, markdownID string, req *requests.UpdateMarkdownRequest) error {
	if strings.TrimSpace(authorID) == "" || strings.TrimSpace(markdownID) == "" || req == nil {
		return ErrMarkdownInvalidInput
	}
	if strings.TrimSpace(req.Title) == "" {
		return fmt.Errorf("%w: 标题不能为空", ErrMarkdownInvalidInput)
	}
	if strings.TrimSpace(req.Content) == "" {
		return fmt.Errorf("%w: 文章内容不能为空", ErrMarkdownInvalidInput)
	}
	if req.Visibility != markdownmodel.VisibilityPublic && req.Visibility != markdownmodel.VisibilityPrivate {
		return fmt.Errorf("%w: 可见性必须为 public 或 private", ErrMarkdownInvalidInput)
	}
	return nil
}
