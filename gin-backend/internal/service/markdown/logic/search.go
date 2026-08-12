package logic

import (
	"context"
	"errors"
	"strings"

	markdownmodel "gin-backend/internal/orm/markdown"
)

// SearchMarkdownLogic 全文搜索当前用户的文章 (标题/摘要/正文)
// 基于 ParadeDB pg_search 的 BM25 智能检索 (jieba 中文分词, 见 ../deployments/postgresql/sql/pg_search_setup.sql)
// 使用 ||| (match disjunction) 操作符: 命中任一查询词即可, 且对原始用户输入安全
// :Param
// - `ctx` 上下文
// - `authorID` 当前登录用户标识 (JWT sub)
// - `keyword` 搜索关键词 (经 jieba 分词后按任意词匹配)
// - `page` 页码 (<=0 时默认 1)
// - `pageSize` 每页条数 (<=0 或 >100 时默认 10)
// :Return
// - `[]*markdownmodel.Markdown` 命中的文章元信息 (按 BM25 相关度倒序)
// - `int64` 命中总数
// - `error` 如果参数非法或查询失败, 返回错误信息
func SearchMarkdownLogic(ctx context.Context, authorID, keyword string, page, pageSize int) ([]*markdownmodel.Markdown, int64, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return nil, 0, errors.New("搜索关键词不能为空")
	}
	if authorID == "" {
		return nil, 0, errors.New("无法识别用户身份")
	}

	db, err := markdownDB(ctx)
	if err != nil {
		return nil, 0, err
	}

	// 归一化分页参数
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 10
	}

	// 1. 命中总数 (||| match disjunction + 软删除/作者过滤, 软删过滤由 gorm 自动附加)
	var total int64
	if err := db.Model(&markdownmodel.Markdown{}).
		Where("author_id = ?", authorID).
		Where("search_text ||| ?", keyword).
		Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// 2. 分页查询, 按 BM25 相关度倒序 (pdb.score 以 key_field 为参)
	//    markdown_id 为 key_field 已索引, 作为并列分的稳定排序兜底
	var list []*markdownmodel.Markdown
	if err := db.Where("author_id = ?", authorID).
		Where("search_text ||| ?", keyword).
		Order("pdb.score(markdown_id) DESC, markdown_id ASC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(&list).Error; err != nil {
		return nil, 0, err
	}

	return list, total, nil
}
