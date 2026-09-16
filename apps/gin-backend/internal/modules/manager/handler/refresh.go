// 管理员刷新 token 对（公开路由；凭据由 sessioncookie 解析）。
package handler

import (
	"net/http"

	eror "gin-backend/internal/common/base/errors"
	"gin-backend/internal/common/base/responses"
	"gin-backend/internal/common/service/sessioncookie"
	logic "gin-backend/internal/modules/manager/logic"

	"github.com/gin-gonic/gin"
)

// RefreshHandler 刷新管理员 token 对: POST /api/v1/public/manager/refresh
func RefreshHandler(c *gin.Context) {
	credential, err := sessioncookie.ResolveManagerRefresh(c)
	if err != nil {
		failRefresh(c, err)
		return
	}

	tokens, err := logic.RefreshLogic(c.Request.Context(), credential.Token, credential.Identity)
	if err != nil {
		responses.Fail(c, http.StatusUnauthorized, eror.CodeUnauthorized, err.Error())
		return
	}
	if err := sessioncookie.SetManagerTokens(c, tokens.AccessToken, tokens.RefreshToken); err != nil {
		responses.Fail(c, http.StatusInternalServerError, eror.CodeInternalError, "刷新会话写入失败")
		return
	}

	responses.OK(c, gin.H{"message": "Token 刷新成功"})
}
