package handler

import (
	"errors"
	"net/http"

	eror "gin-backend/internal/common/base/errors"
	"gin-backend/internal/common/base/responses"
	"gin-backend/internal/common/service/sessioncookie"
	tc "gin-backend/internal/modules/manager/types/constant"

	"github.com/gin-gonic/gin"
)

// 本文件是 manager HTTP 适配层唯一的错误出口映射位置: handler 内不再出现
// switch errors.Is(...) 形式的映射逻辑。

// failRefresh 把刷新凭据解析错误映射为统一的 HTTP 失败响应。
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

// failReview 把审批流错误映射为统一的 HTTP 失败响应。
// "用户名已被占用" 仅对通过申请有意义, 由 includeUsernameTaken 控制。
func failReview(c *gin.Context, err error, includeUsernameTaken bool) {
	switch {
	case errors.Is(err, tc.ErrRequestNotFound):
		responses.Fail(c, http.StatusNotFound, eror.CodeNotFound, "申请单不存在")
	case errors.Is(err, tc.ErrRequestReviewed):
		responses.Fail(c, http.StatusConflict, eror.CodeConflict, "申请单已审批")
	case includeUsernameTaken && errors.Is(err, tc.ErrUsernameTaken):
		responses.Fail(c, http.StatusConflict, eror.CodeConflict, "用户名已被占用")
	default:
		responses.Fail(c, http.StatusInternalServerError, eror.CodeInternalError, "审批失败")
	}
}
