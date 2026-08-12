package requests

// UploadMarkdownRequest 上传文章请求参数
// Visibility: 可见性 (public 公开 / private 私有), 缺省 private
type UploadMarkdownRequest struct {
	Title      string `json:"title" binding:"required,min=1,max=255"`
	Content    string `json:"content" binding:"required"`
	Visibility string `json:"visibility" binding:"omitempty,oneof=public private"`
}

// ListMyMarkdownQuery 我的文章列表查询参数
type ListMyMarkdownQuery struct {
	Page     int `form:"page" binding:"omitempty,min=1"`
	PageSize int `form:"pageSize" binding:"omitempty,min=1,max=100"`
}

// SearchMarkdownQuery 全文搜索查询参数
// Keyword 支持 websearch 语法: 引号精确短语 / OR / 排除符 -
type SearchMarkdownQuery struct {
	Keyword  string `form:"keyword" binding:"required"`
	Page     int    `form:"page" binding:"omitempty,min=1"`
	PageSize int    `form:"pageSize" binding:"omitempty,min=1,max=100"`
}
