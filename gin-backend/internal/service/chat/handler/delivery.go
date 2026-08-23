package handler

import (
	"net/http"

	eror "gin-backend/internal/common/base/errors"
	"gin-backend/internal/common/base/responses"
	"gin-backend/internal/service/chat"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const maxAckBatchSize = 100

type ackDeliveryRequest struct {
	DeliveryIDs []string `json:"deliveryIds" binding:"required,min=1,max=100"`
}

// AckDeliveryHandler 批量确认消息已被客户端应用处理；重复 ACK 保持幂等。
func AckDeliveryHandler(c *gin.Context) {
	subject, ok := currentSubject(c)
	if !ok {
		responses.Fail(c, http.StatusUnauthorized, eror.CodeUnauthorized, "无效的Token")
		return
	}
	var req ackDeliveryRequest
	if err := c.ShouldBindJSON(&req); err != nil || len(req.DeliveryIDs) > maxAckBatchSize {
		responses.Fail(c, http.StatusBadRequest, eror.CodeValidationFailed, "deliveryIds 必须包含 1 到 100 个投递 ID")
		return
	}
	deliveryIDs := make([]string, 0, len(req.DeliveryIDs))
	seen := make(map[string]struct{}, len(req.DeliveryIDs))
	for _, id := range req.DeliveryIDs {
		if _, err := uuid.Parse(id); err != nil {
			responses.Fail(c, http.StatusBadRequest, eror.CodeValidationFailed, "投递 ID 格式无效")
			return
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		deliveryIDs = append(deliveryIDs, id)
	}
	service := chat.Service()
	if service == nil {
		responses.Fail(c, http.StatusServiceUnavailable, eror.CodeServiceUnavail, "聊天服务暂不可用")
		return
	}
	if err := service.Ack(c.Request.Context(), subject, deliveryIDs); err != nil {
		responses.Fail(c, http.StatusServiceUnavailable, eror.CodeServiceUnavail, "确认服务暂不可用")
		return
	}
	c.Status(http.StatusNoContent)
}
