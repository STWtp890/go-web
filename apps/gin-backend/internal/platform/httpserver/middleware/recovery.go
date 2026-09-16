package middleware

import (
	"log/slog"
	"net/http"
	"runtime"

	eror "gin-backend/internal/common/base/errors"
	"gin-backend/internal/common/base/responses"

	"github.com/gin-gonic/gin"
)

// GinRecovery 中间件, 用于捕获 panic 并返回 500 错误。
// 出口统一经 responses.AbortFail, 保证 panic 响应与其它失败响应使用同一信封。
func GinRecovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				buf := make([]byte, 4096)
				n := runtime.Stack(buf, false)

				slog.Error("panic_recovered",
					slog.Any("panic", r),
					slog.String("stack", string(buf[:n])),
					slog.String("method", c.Request.Method),
					slog.String("path", c.Request.URL.Path),
				)

				responses.AbortFail(c, http.StatusInternalServerError, eror.CodeInternalError, "服务器内部错误")
			}
		}()
		c.Next()
	}
}
