package handler

import (
	"net/http"

	eror "gin-backend/internal/common/base/errors"
	"gin-backend/internal/common/base/responses"
	"gin-backend/internal/common/service/jwtmethod"
	logic "gin-backend/internal/service/chat/logic"

	"github.com/gin-gonic/gin"
)

// SSEHandler SSE 流入口: GET /api/v1/protected/chat/sse
func SSEHandler(c *gin.Context) {
	// 鉴权 (AuthRequired 中间件已校验, 此处提取 subject)
	claims, exists := jwtmethod.ExtractClaims(c)
	if !exists {
		responses.Fail(c, http.StatusUnauthorized, eror.CodeUnauthorized, "无效的Token")
		return
	}
	sub, err := claims.GetSubject()
	if err != nil {
		responses.Fail(c, http.StatusUnauthorized, eror.CodeUnauthorized, "无效的Token")
		return
	}

	// 断点续传 ID: 优先 Last-Event-ID 请求头 (EventSource 重连时自动携带),
	// 备选 query 参数 (手动调试用)
	lastEventID := c.GetHeader("Last-Event-ID")
	if lastEventID == "" {
		lastEventID = c.Query("Last-Event-ID")
	}

	// 进入 SSE 长连接 (阻塞直至断开)
	logic.SSELogic(c.Request.Context(), sub, lastEventID, c.Writer)
}
