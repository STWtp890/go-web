package handler

import (
	"net/http"

	eror "gin-backend/internal/common/base/errors"
	"gin-backend/internal/common/base/responses"
	"gin-backend/internal/common/service/sessioncookie"
	logic "gin-backend/internal/modules/auth/logic"

	"github.com/gin-gonic/gin"
)

// RefreshTokenHandler 刷新令牌
func RefreshTokenHandler(c *gin.Context) {
	credential, err := sessioncookie.ResolveUserRefresh(c)
	if err != nil {
		failRefresh(c, err)
		return
	}

	tokens, err := logic.RefreshTokenLogic(c.Request.Context(), credential.Token, credential.Identity)
	if err != nil {
		responses.Fail(c, http.StatusUnauthorized, eror.CodeUnauthorized, err.Error())
		return
	}
	if err := sessioncookie.SetUserTokens(c, tokens.AccessToken, tokens.RefreshToken); err != nil {
		responses.Fail(c, http.StatusInternalServerError, eror.CodeInternalError, "刷新会话写入失败")
		return
	}

	responses.OK(c, gin.H{"message": "Token 刷新成功"})
}
