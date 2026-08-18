package handler

import (
	"net/http"

	eror "gin-backend/internal/common/base/errors"
	"gin-backend/internal/common/base/responses"
	"gin-backend/internal/service/chat"

	"github.com/gin-gonic/gin"
)

// AcknowledgeDeliveryHandler 确认消息已被客户端处理；重复确认保持幂等。
func AcknowledgeDeliveryHandler(c *gin.Context) {
	subject, ok := currentSubject(c)
	if !ok {
		responses.Fail(c, http.StatusUnauthorized, eror.CodeUnauthorized, "无效的Token")
		return
	}
	id := c.Param("deliveryId")
	if id == "" {
		responses.Fail(c, http.StatusBadRequest, eror.CodeValidationFailed, "缺少投递 ID")
		return
	}
	h := chat.SSEHub()
	if h == nil {
		responses.Fail(c, http.StatusServiceUnavailable, eror.CodeServiceUnavail, "聊天服务暂不可用")
		return
	}
	ok, err := h.Bridge().Acknowledge(c.Request.Context(), subject, id)
	if err != nil {
		responses.Fail(c, http.StatusServiceUnavailable, eror.CodeServiceUnavail, "确认服务暂不可用")
		return
	}
	if !ok {
		responses.Fail(c, http.StatusNotFound, eror.CodeNotFound, "投递不存在")
		return
	}
	c.Status(http.StatusNoContent)
}
