package middleware

import (
	"log/slog"
	"time"

	"gin-backend/internal/common/base/constant"

	"github.com/gin-gonic/gin"
)

// GinLogger 中间件用 slog 记录每次 HTTP 请求（替代 gin.Logger()）
func GinLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 记求开始
		start := time.Now()
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery

		c.Next()

		// 请求结束
		latency := time.Since(start)
		status := c.Writer.Status()

		// 构建日志属性
		attrs := []slog.Attr{
			slog.Int("status", status),
			slog.String("method", c.Request.Method),
			slog.String("path", path),
			slog.Duration("latency", latency),
			slog.String("client_ip", c.ClientIP()),
			slog.Int("body_size", c.Writer.Size()),
			slog.String("user_agent", c.Request.UserAgent()),
		}

		// 注入 trace_id
		if tid := c.GetString(constant.CtxKeyTraceID); tid != "" {
			attrs = append(
				[]slog.Attr{slog.String("trace_id", tid)},
				attrs...,
			)
		}

		if query != "" {
			attrs = append(attrs, slog.String("query", query))
		}

		switch {
		case status >= 500:
			slog.Error("request", toAnySlice(attrs)...)
		case status >= 400:
			slog.Warn("request", toAnySlice(attrs)...)
		default:
			slog.Info("request", toAnySlice(attrs)...)
		}
	}
}

// toAnySlice 将 []slog.Attr 转为 ...any（slog 可变参数要求 key-value 对）
func toAnySlice(attrs []slog.Attr) []any {
	result := make([]any, 0, len(attrs)*2)
	for _, a := range attrs {
		result = append(result, a.Key, a.Value.Any())
	}
	return result
}
