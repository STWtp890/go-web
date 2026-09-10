package logic

import "errors"

// Markdown 业务错误用于在 HTTP 层稳定映射状态码，同时避免暴露数据库错误。
var (
	ErrMarkdownInvalidInput = errors.New("markdown: 输入参数无效")
	ErrMarkdownNotFound     = errors.New("markdown: 文章不存在")
	ErrMarkdownForbidden    = errors.New("markdown: 无权操作该文章")
)
