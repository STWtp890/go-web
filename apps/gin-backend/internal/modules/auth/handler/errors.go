package handler

import (
	"errors"
	"net/http"

	eror "gin-backend/internal/common/base/errors"
	"gin-backend/internal/common/base/responses"
	"gin-backend/internal/common/service/sessioncookie"

	"github.com/gin-gonic/gin"
)

// failRefresh 把刷新凭据解析错误映射为统一的 HTTP 失败响应。
//
// 本文件是 auth HTTP 适配层唯一的错误出口映射位置: handler 内不再出现
// switch errors.Is(...) 形式的映射逻辑。
func failRefresh(c *gin.Context, err error) {
	switch {
	case errors.Is(err, sessioncookie.ErrRefreshTokenMissing),
		errors.Is(err, sessioncookie.ErrRefreshTokenInvalid):
		responses.Fail(c, http.StatusUnauthorized, eror.CodeUnauthorized, "无效的Token")
	case errors.Is(err, sessioncookie.ErrRefreshCSRFInvalid):
		responses.Fail(c, http.StatusForbidden, eror.CodeForbidden, "CSRF 校验失败")
	default:
		responses.Fail(c, http.StatusInternalServerError, eror.CodeInternalError, "刷新会话读取失败")
	}
}
