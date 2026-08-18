package logic

import (
	"context"
	"errors"
	"strings"

	"gin-backend/internal/common/base/connection/postgresql"
	markdownmodel "gin-backend/internal/model/orm/markdown"
	"gin-backend/internal/model/store"
	"gin-backend/internal/service/markdown/types/requests"
	"gin-backend/internal/service/markdown/utils"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// UploadMarkdownLogic 上传文章: 生成对象ID + 摘要, 元信息与内容同事务写入
// :Param
// - `ctx` 上下文
// - `authorID` 当前登录用户标识 (JWT sub)
// - `req` 上传请求 (title + content)
// :Return
// - `*markdownmodel.Markdown` 创建成功的文章元信息
// - `error` 如果参数非法或写入失败, 返回错误信息
func UploadMarkdownLogic(ctx context.Context, authorID string, req *requests.UploadMarkdownRequest) (*markdownmodel.Markdown, error) {
	// 1. 参数校验
	if authorID == "" {
		return nil, errors.New("无法识别用户身份")
	}
	if strings.TrimSpace(req.Title) == "" {
		return nil, errors.New("标题不能为空")
	}
	if strings.TrimSpace(req.Content) == "" {
		return nil, errors.New("文章内容不能为空")
	}

	db, err := utils.MarkdownDB(ctx)
	if err != nil {
		return nil, err
	}

	// 2. 组装数据 (markdown_id 对外暴露, 防遍历)
	//    SearchText = 标题 + 摘要 + 正文, 供 pg_search 全文检索索引
	title := strings.TrimSpace(req.Title)
	summary := utils.BuildSummary(req.Content)
	visibility := req.Visibility
	if visibility == "" {
		visibility = markdownmodel.VisibilityPrivate // 缺省私有
	}
	md := &markdownmodel.Markdown{
		MarkdownID: uuid.NewString(),
		AuthorID:   authorID,
		Title:      title,
		Summary:    summary,
		Visibility: visibility,
		SearchText: title + " " + summary + " " + req.Content,
	}
	content := &markdownmodel.Content{
		MarkdownID: md.MarkdownID,
		Content:    req.Content,
	}

	// 3. 事务写入元信息 + 内容 (任一失败整体回滚)
	err = postgresql.Transaction(db, func(tx *gorm.DB) error {
		if err := tx.Create(md).Error; err != nil {
			return err
		}
		return tx.Create(content).Error
	})
	if err != nil {
		return nil, err
	}

	// 4. 失效缓存 (新文章下次详情回源新数据; Cache-Aside 一致性, 尽力而为)
	_ = store.Markdown.Evict(ctx, md.MarkdownID)

	return md, nil
}
