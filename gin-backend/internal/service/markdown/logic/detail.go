package logic

import (
	"context"
	"errors"

	markdownmodel "gin-backend/internal/orm/markdown"

	"gorm.io/gorm"
)

// GetMarkdownLogic 获取文章详情 (元信息 + 完整 content)
// 归属校验: 仅当前登录用户 (authorID) 可读取自己的文章, 防越权 (IDOR)
// :Param
// - `ctx` 上下文
// - `authorID` 当前登录用户标识 (JWT sub)
// - `markdownID` 文章对象ID (UUID)
// :Return
// - `*markdownmodel.Markdown` 文章元信息
// - `string` 完整 Markdown 正文
// - `error` 如果文章不存在或查询失败, 返回错误信息
func GetMarkdownLogic(ctx context.Context, authorID, markdownID string) (*markdownmodel.Markdown, string, error) {
	if authorID == "" {
		return nil, "", errors.New("无法识别用户身份")
	}
	db, err := markdownDB(ctx)
	if err != nil {
		return nil, "", err
	}

	// 1. 查询元信息 (软删除过滤由 gorm 自动附加 + 归属校验)
	var md markdownmodel.Markdown
	err = db.Where("markdown_id = ? AND author_id = ?", markdownID, authorID).First(&md).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, "", errors.New("文章不存在")
		}
		return nil, "", err
	}

	// 2. 查询正文内容
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
