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
	Page       int `json:"page,omitempty"`
	PerPage    int `json:"per_page,omitempty"`
	Total      int `json:"total,omitempty"`
	TotalPages int `json:"total_pages,omitempty"`
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
