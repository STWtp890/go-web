package middleware

import (
	"gin-backend/internal/common/base/constant"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// TraceID 为每个请求注入 trace_id
func TraceID() gin.HandlerFunc {
	return func(c *gin.Context) {
		traceID := c.Request.Header.Get("X-Trace-ID")
		if traceID == "" {
			traceID = uuid.New().String()
		}
		c.Set(constant.CtxKeyTraceID, traceID)
		c.Header("X-Trace-ID", traceID)
		c.Next()
	}
}
