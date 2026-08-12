package logic

import (
	"context"
	"errors"

	markdownmodel "gin-backend/internal/orm/markdown"

	"gorm.io/gorm"
)

// 可见性/归属错误
var (
	// ErrMarkdownForbidden 无权查看该文章 (private 非作者)
	ErrMarkdownForbidden = errors.New("markdown: 无权查看该文章")
)

// GetMarkdownLogic 获取文章详情 (元信息 + 完整 content)
// 可见性校验: public 任意登录用户可读; private 仅作者本人可读 (防越权 IDOR)
// :Param
// - `ctx` 上下文
// - `viewerID` 当前登录用户标识 (JWT sub)
// - `markdownID` 文章对象ID (UUID)
// :Return
// - `*markdownmodel.Markdown` 文章元信息
// - `string` 完整 Markdown 正文
// - `error` 文章不存在 / 无权查看 / 查询失败
func GetMarkdownLogic(ctx context.Context, viewerID, markdownID string) (*markdownmodel.Markdown, string, error) {
	if viewerID == "" {
		return nil, "", errors.New("无法识别用户身份")
	}
	db, err := markdownDB(ctx)
	if err != nil {
		return nil, "", err
	}

	// 1. 查询元信息 (软删除过滤由 gorm 自动附加)
	var md markdownmodel.Markdown
	err = db.Where("markdown_id = ?", markdownID).First(&md).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, "", errors.New("文章不存在")
		}
		return nil, "", err
	}

	// 2. 可见性校验: private 仅作者; public 任意登录用户
	if md.Visibility == markdownmodel.VisibilityPrivate && md.AuthorUserID != viewerID {
		return nil, "", ErrMarkdownForbidden
	}

	// 3. 查询正文内容
	var content markdownmodel.Content
	err = db.Where("markdown_id = ?", markdownID).First(&content).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, "", errors.New("文章内容不存在")
		}
		return nil, "", err
	}

	return &md, content.Content, nil
}
