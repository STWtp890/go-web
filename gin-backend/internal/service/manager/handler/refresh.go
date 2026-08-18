// 管理员刷新 token 对（公开路由；Handler 内部验签、角色与 sid）。
package handler

import (
	"net/http"

	eror "gin-backend/internal/common/base/errors"
	"gin-backend/internal/common/base/responses"
	"gin-backend/internal/common/service/jwt"
	logic "gin-backend/internal/service/manager/logic"

	"github.com/gin-gonic/gin"
)

// RefreshHandler 刷新管理员 token 对: POST /api/v1/public/manager/refresh
// 请求头 Authorization: Bearer <refreshToken>; 响应: {accessToken, refreshToken}
func RefreshHandler(c *gin.Context) {
	token := jwt.ExtractToken(c)
	if token == "" {
		responses.Fail(c, http.StatusUnauthorized, eror.CodeUnauthorized, "无效的Token")
		return
	}

	tokenMap, err := logic.RefreshLogic(c.Request.Context(), token)
	if err != nil {
		responses.Fail(c, http.StatusUnauthorized, eror.CodeUnauthorized, err.Error())
		return
	}

	responses.OK(c, gin.H{
		"accessToken":  (*tokenMap)["accessToken"],
		"refreshToken": (*tokenMap)["refreshToken"],
	})
}
