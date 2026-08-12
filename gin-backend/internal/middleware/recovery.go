package middleware

import (
    "log/slog"
    "runtime"

    "github.com/gin-gonic/gin"
)

// GinRecovery 中间件, 用于捕获 panic 并返回 500 错误
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

                c.AbortWithStatusJSON(500, gin.H{
                    "success": false,
                    "error":   gin.H{"code": "INTERNAL_ERROR", "message": "服务器内部错误"},
                })
            }
        }()
        c.Next()
    }
}