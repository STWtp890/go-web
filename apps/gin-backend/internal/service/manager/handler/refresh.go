// 管理员刷新 token 对（公开路由；Handler 内部验签、角色与 sid）。
package handler

import (
	"net/http"

	eror "gin-backend/internal/common/base/errors"
	"gin-backend/internal/common/base/responses"
	"gin-backend/internal/common/service/sessioncookie"
	logic "gin-backend/internal/service/manager/logic"

	"github.com/gin-gonic/gin"
)

// RefreshHandler 刷新管理员 token 对: POST /api/v1/public/manager/refresh
func RefreshHandler(c *gin.Context) {
	token := sessioncookie.RefreshToken(c, sessioncookie.ManagerRefreshCookie)
	if token == "" {
		responses.Fail(c, http.StatusUnauthorized, eror.CodeUnauthorized, "无效的Token")
		return
	}
	if !sessioncookie.ValidateRefreshCSRF(c, sessioncookie.ManagerCSRFCookie, token) {
		responses.Fail(c, http.StatusForbidden, eror.CodeForbidden, "CSRF 校验失败")
		return
	}

	tokenMap, err := logic.RefreshLogic(c.Request.Context(), token)
	if err != nil {
		responses.Fail(c, http.StatusUnauthorized, eror.CodeUnauthorized, err.Error())
		return
	}
	if err := sessioncookie.SetManagerTokens(c, (*tokenMap)["accessToken"], (*tokenMap)["refreshToken"]); err != nil {
		responses.Fail(c, http.StatusInternalServerError, eror.CodeInternalError, "刷新会话写入失败")
		return
	}

	responses.OK(c, gin.H{"message": "Token 刷新成功"})
}
