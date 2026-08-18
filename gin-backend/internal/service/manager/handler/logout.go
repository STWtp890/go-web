// 管理员登出 (ManagerAuthRequired 保护): access 黑名单 + refresh 白名单删除
package handler

import (
	"net/http"

	eror "gin-backend/internal/common/base/errors"
	"gin-backend/internal/common/base/responses"
	logic "gin-backend/internal/service/manager/logic"

	"github.com/gin-gonic/gin"
	jwtlib "github.com/golang-jwt/jwt/v5"
)

// LogoutHandler 管理员登出: POST /api/v1/protected/manager/logout
// 条件删除当前 sid 会话后，Access / Refresh Token 均立即失效。
func LogoutHandler(c *gin.Context) {
	// 提取中间件注入的 claims
	claims, ok := c.Get("claims")
	if !ok {
		responses.Fail(c, http.StatusUnauthorized, eror.CodeUnauthorized, "未登录")
		return
	}
	mapClaims, ok := claims.(jwtlib.MapClaims)
	if !ok {
		responses.Fail(c, http.StatusInternalServerError, eror.CodeInternalError, "token 解析异常")
		return
	}

	// 执行登出 (吊销)
	if err := logic.LogoutLogic(c.Request.Context(), mapClaims); err != nil {
		responses.Fail(c, http.StatusInternalServerError, eror.CodeInternalError, err.Error())
		return
	}

	responses.OK(c, gin.H{"message": "登出成功"})
}
