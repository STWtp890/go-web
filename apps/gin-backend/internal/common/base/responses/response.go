package responses

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Response is the standard API envelope.
type Response struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   *ErrorInfo  `json:"error,omitempty"`
	Meta    *Meta       `json:"meta,omitempty"`
}

// ErrorInfo provides insensitive details about an error.
type ErrorInfo struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Meta provides pagination and other metadata for responses.
type Meta struct {
	Page       int `json:"page"`
	PerPage    int `json:"per_page"`
	Total      int `json:"total"`
	TotalPages int `json:"total_pages"`
	// NextCursor is the opaque forward cursor of a cursor-paginated list. It is
	// omitted when there is no next page, so a client can never mistake "no more
	// results" for an empty-but-present cursor, and it is never derived from a
	// page number: the owning service is the only party that can name a position
	// in its own ordering.
	NextCursor string `json:"nextCursor,omitempty"`
	// Truncated reports that the answer is not the complete result set, as the
	// search service defines it after F02: results exist beyond what this
	// response shows. It is omitted when false so a client can treat its presence
	// as the signal to tell the user "you are not seeing everything".
	Truncated bool `json:"truncated,omitempty"`
}

// NewPageMeta 构造统一的分页元信息, 收敛各模块自行计算 total_pages 的差异。
// perPage <= 0 回退为 1 以避免除零; page <= 0 回退为 1; total < 0 视为 0。
func NewPageMeta(page, perPage, total int) *Meta {
	if page <= 0 {
		page = 1
	}
	if perPage <= 0 {
		perPage = 1
	}
	if total < 0 {
		total = 0
	}
	totalPages := 0
	if total > 0 {
		totalPages = (total + perPage - 1) / perPage
	}
	return &Meta{Page: page, PerPage: perPage, Total: total, TotalPages: totalPages}
}

// NewCursorPageMeta 构造游标分页的元信息。
//
// page 只是客户端当前展示的页码（用于渲染“第 N 页”），不参与数据选取：数据由
// nextCursor 对应的服务端位置决定。total 使用来源服务的真实总数，total_pages 由
// 真实总数计算，因此客户端不需要也不应该自行猜测。
func NewCursorPageMeta(page, perPage int, total int64, nextCursor string) *Meta {
	if page <= 0 {
		page = 1
	}
	if perPage <= 0 {
		perPage = 1
	}
	if total < 0 {
		total = 0
	}
	totalPages := 0
	if total > 0 {
		totalPages = int((total + int64(perPage) - 1) / int64(perPage))
	}
	return &Meta{Page: page, PerPage: perPage, Total: int(total), TotalPages: totalPages, NextCursor: nextCursor}
}

/* 函数 */

// OK 返回成功响应
func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, Response{
		Success: true,
		Data:    data,
	})
}

// Created 返回创建成功响应
func Created(c *gin.Context, data any) {
	c.JSON(http.StatusCreated, Response{
		Success: true,
		Data:    data,
	})
}

// OKWithMeta 返回带元信息的成功响应（分页等）
func OKWithMeta(c *gin.Context, data any, meta *Meta) {
	c.JSON(http.StatusOK, Response{
		Success: true,
		Data:    data,
		Meta:    meta,
	})
}

// Fail 返回错误响应
func Fail(c *gin.Context, status int, code, message string) {
	c.JSON(status, Response{
		Success: false,
		Error:   &ErrorInfo{Code: code, Message: message},
	})
}

// AbortFail 终止当前 Gin handler chain 并返回统一失败响应。
// 鉴权、授权等中间件必须使用该函数，避免仅 return 当前中间件后继续执行业务 Handler。
func AbortFail(c *gin.Context, status int, code, message string) {
	c.Abort()
	Fail(c, status, code, message)
}

// FailAppError 根据 AppError 返回错误响应
func FailAppError(c *gin.Context, err error) {
	if appErr, ok := err.(interface {
		HTTPStatus() int
		Code() string
		Message() string
	}); ok {
		c.JSON(appErr.HTTPStatus(), Response{
			Success: false,
			Error:   &ErrorInfo{Code: appErr.Code(), Message: appErr.Message()},
		})
		return
	}
	// fallback
	c.JSON(http.StatusInternalServerError, Response{
		Success: false,
		Error:   &ErrorInfo{Code: "INTERNAL_ERROR", Message: err.Error()},
	})
}
