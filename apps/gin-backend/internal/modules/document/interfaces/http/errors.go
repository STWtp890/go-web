// Package httpadapter 的错误出口映射。
//
// 本文件是 document HTTP 适配层唯一的错误映射位置: handler 内不再出现
// switch errors.Is(...) 形式的映射逻辑。
package httpadapter

import (
	"errors"
	"net/http"

	baseerrors "gin-backend/internal/common/base/errors"
	"gin-backend/internal/common/base/responses"
	"gin-backend/internal/modules/document/application"

	"github.com/gin-gonic/gin"
)

// writeUnauthorized 返回统一的未认证失败响应。
func writeUnauthorized(c *gin.Context) {
	responses.Fail(c, http.StatusUnauthorized, baseerrors.CodeUnauthorized, "无法识别用户身份")
}

// writeApplicationError 把 document 应用层错误映射为统一的 HTTP 失败响应。
// internalMessage 用于未预期错误, 避免把内部细节直接暴露给调用方。
func writeApplicationError(c *gin.Context, err error, internalMessage string) {
	switch {
	case errors.Is(err, application.ErrInvalidInput), errors.Is(err, application.ErrQueryInvalidInput):
		responses.Fail(c, http.StatusBadRequest, baseerrors.CodeValidationFailed, err.Error())
	case errors.Is(err, application.ErrDocumentNotFound), errors.Is(err, application.ErrDocumentNotActive), errors.Is(err, application.ErrQueryNotFound):
		responses.Fail(c, http.StatusNotFound, baseerrors.CodeNotFound, "文章不存在")
	case errors.Is(err, application.ErrDocumentForbidden), errors.Is(err, application.ErrQueryForbidden):
		responses.Fail(c, http.StatusForbidden, baseerrors.CodeForbidden, "无权操作该文章")
	default:
		responses.Fail(c, http.StatusInternalServerError, baseerrors.CodeInternalError, internalMessage)
	}
}
