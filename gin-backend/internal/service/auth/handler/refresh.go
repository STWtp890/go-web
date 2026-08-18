package handler

import (
	"net/http"

	eror "gin-backend/internal/common/base/errors"
	"gin-backend/internal/common/base/responses"
	"gin-backend/internal/common/service/jwt"
	logic "gin-backend/internal/service/auth/logic"

	"github.com/gin-gonic/gin"
)

// RefreshTokenHandler 刷新令牌
func RefreshTokenHandler(c *gin.Context) {
	token := jwt.ExtractToken(c)

	tokenMap, err := logic.RefreshTokenLogic(c.Request.Context(), token)
	if err != nil {
		responses.Fail(c, http.StatusUnauthorized, eror.CodeUnauthorized, err.Error())
		return
	}

	responses.OK(c, gin.H{
		"accessToken":  (*tokenMap)["accessToken"],
		"refreshToken": (*tokenMap)["refreshToken"],
	})
}
