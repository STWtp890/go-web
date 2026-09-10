package handler

import (
	"net/http"

	eror "gin-backend/internal/common/base/errors"
	"gin-backend/internal/common/base/responses"
	"gin-backend/internal/common/service/sessioncookie"
	logic "gin-backend/internal/service/auth/logic"

	"github.com/gin-gonic/gin"
)

// RefreshTokenHandler 刷新令牌
func RefreshTokenHandler(c *gin.Context) {
	token := sessioncookie.RefreshToken(c, sessioncookie.UserRefreshCookie)
	if token == "" {
		responses.Fail(c, http.StatusUnauthorized, eror.CodeUnauthorized, "无效的Token")
		return
	}
	if !sessioncookie.ValidateRefreshCSRF(c, sessioncookie.UserCSRFCookie, token) {
		responses.Fail(c, http.StatusForbidden, eror.CodeForbidden, "CSRF 校验失败")
		return
	}

	tokenMap, err := logic.RefreshTokenLogic(c.Request.Context(), token)
	if err != nil {
		responses.Fail(c, http.StatusUnauthorized, eror.CodeUnauthorized, err.Error())
		return
	}
	if err := sessioncookie.SetUserTokens(c, (*tokenMap)["accessToken"], (*tokenMap)["refreshToken"]); err != nil {
		responses.Fail(c, http.StatusInternalServerError, eror.CodeInternalError, "刷新会话写入失败")
		return
	}

	responses.OK(c, gin.H{"message": "Token 刷新成功"})
}
