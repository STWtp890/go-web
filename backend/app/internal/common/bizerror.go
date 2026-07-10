package common

import "fmt"

// BizError 携带 HTTP 状态码的业务错误。
// logic 层 return nil, BizError → handler ErrorCtx → SetErrorHandlerCtx → 正确 HTTP 状态码 + JSON body。
type BizError struct {
	Code       int    `json:"code"`
	Message    string `json:"message"`
	HTTPStatus int    `json:"-"`
}

func (e *BizError) Error() string {
	return fmt.Sprintf("[%d] %s", e.Code, e.Message)
}

// NewBizError 创建 BizError，自动将 code 映射为 HTTP 状态码。
func NewBizError(code int, message string) *BizError {
	return &BizError{
		Code:       code,
		Message:    message,
		HTTPStatus: code,
	}
}
