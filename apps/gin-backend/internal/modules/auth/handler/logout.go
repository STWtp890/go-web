package handler

import (
	"net/http"

	eror "gin-backend/internal/common/base/errors"
	"gin-backend/internal/common/base/responses"
	"gin-backend/internal/common/service/sessioncookie"
	logic "gin-backend/internal/modules/auth/logic"
	"gin-backend/internal/platform/httpserver/identity"

	"github.com/gin-gonic/gin"
)

// LogoutHandler 用户登出
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
	sessioncookie.ClearUserTokens(c)

	responses.OK(c, gin.H{"message": "登出成功"})
}
