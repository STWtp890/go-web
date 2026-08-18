package errors

import "net/http"

// AppError 应用错误结构
type AppError struct {
	HTTPStatus int    `json:"-"`
	Code       string `json:"code"`
	Message    string `json:"message"`
	Err        error  `json:"-"`
}

func (e *AppError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return e.Message
}

// 预定义错误码
const (
	CodeBadRequest       = "BAD_REQUEST"
	CodeUnauthorized     = "UNAUTHORIZED"
	CodeForbidden        = "FORBIDDEN"
	CodeNotFound         = "NOT_FOUND"
	CodeConflict         = "CONFLICT"
	CodeParseError       = "PARSE_ERROR"
	CodeValidationFailed = "VALIDATION_FAILED"
	CodeInternalError    = "INTERNAL_ERROR"
	CodeServiceUnavail   = "SERVICE_UNAVAILABLE"
)

// 预定义错误工厂函数
func NewBadRequest(msg string) *AppError {
	return &AppError{HTTPStatus: http.StatusBadRequest, Code: CodeBadRequest, Message: msg}
}

func NewUnauthorized(msg string) *AppError {
	return &AppError{HTTPStatus: http.StatusUnauthorized, Code: CodeUnauthorized, Message: msg}
}

func NewForbidden(msg string) *AppError {
	return &AppError{HTTPStatus: http.StatusForbidden, Code: CodeForbidden, Message: msg}
}

func NewNotFound(msg string) *AppError {
	return &AppError{HTTPStatus: http.StatusNotFound, Code: CodeNotFound, Message: msg}
}

func NewConflict(msg string) *AppError {
	return &AppError{HTTPStatus: http.StatusConflict, Code: CodeConflict, Message: msg}
}

func NewParseError(msg string) *AppError {
	return &AppError{HTTPStatus: http.StatusBadRequest, Code: CodeParseError, Message: msg}
}

func NewValidationFailed(msg string) *AppError {
	return &AppError{HTTPStatus: http.StatusBadRequest, Code: CodeValidationFailed, Message: msg}
}

func NewInternalError(msg string) *AppError {
	return &AppError{HTTPStatus: http.StatusInternalServerError, Code: CodeInternalError, Message: msg}
}

func Wrap(err error, httpStatus int, code, msg string) *AppError {
	return &AppError{HTTPStatus: httpStatus, Code: code, Message: msg, Err: err}
}
