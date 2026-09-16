// 管理员登出 (ManagerAuthRequired 保护): 条件删除当前 sid 会话
package handler

import (
	"net/http"

	eror "gin-backend/internal/common/base/errors"
	"gin-backend/internal/common/base/responses"
	"gin-backend/internal/common/service/sessioncookie"
	logic "gin-backend/internal/modules/manager/logic"
	"gin-backend/internal/platform/httpserver/identity"

	"github.com/gin-gonic/gin"
)

// LogoutHandler 管理员登出: POST /api/v1/protected/manager/logout
// 条件删除当前 sid 会话后，Access / Refresh Token 均立即失效。
func LogoutHandler(c *gin.Context) {
	principal, ok := identity.FromGin(c)
	if !ok {
		responses.Fail(c, http.StatusUnauthorized, eror.CodeUnauthorized, "未登录")
		return
	}

	if err := logic.LogoutLogic(c.Request.Context(), principal.Subject, principal.SessionID); err != nil {
		responses.Fail(c, http.StatusInternalServerError, eror.CodeInternalError, err.Error())
		return
	}
	sessioncookie.ClearManagerTokens(c)

	responses.OK(c, gin.H{"message": "登出成功"})
}
