package handler

import (
	"net/http"

	eror "gin-backend/internal/common/base/errors"
	"gin-backend/internal/common/base/responses"
	logic "gin-backend/internal/service/auth/logic"
	req "gin-backend/internal/service/auth/types/requests"

	"github.com/gin-gonic/gin"
)

// RegisterHandler 用户注册
func RegisterHandler(c *gin.Context) {
	req := new(req.RegisterRequest)
	if err := c.ShouldBindJSON(req); err != nil {
		responses.Fail(c, http.StatusBadRequest, eror.CodeValidationFailed, "请检查输入参数")
		return
	}

	user, err := logic.RegisterLogic(c.Request.Context(), req)
	if err != nil {
		responses.Fail(c, http.StatusConflict, eror.CodeConflict, err.Error())
		return
	}

	responses.Created(c, user)
}
