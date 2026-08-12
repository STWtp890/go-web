package logger

import (
	"log/slog"
	"os"
	"strings"

	"gin-backend/internal/config/must"
)

// NewLogger 根据配置创建 slog.Logger
func NewLogger(cfg must.LogConfig) *slog.Logger {
	opts := &slog.HandlerOptions{
		Level:     parseLevel(cfg.Level),
		AddSource: cfg.Level == "debug", // debug 模式显示调用位置
	}

	writer := buildWriter(cfg)

	var handler slog.Handler
	switch cfg.Format {
	case "json":
		handler = slog.NewJSONHandler(writer, opts)
	default:
		handler = slog.NewTextHandler(writer, opts)
	}

	// 如果配置了输出到文件，包装成双写（stdout + file）
	if cfg.Output == "file" || cfg.Output == "both" {
		handler = slog.NewMultiHandler(
			slog.NewTextHandler(os.Stdout, opts),
			handler,
		)
	}

	return slog.New(handler)
}

// NewDefaultLogger 创建默认 logger（控制台 text 格式）
func NewDefaultLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level:     slog.LevelInfo,
		AddSource: true,
	}))
}

// Logger 获取 `log/slog` 全局默认 logger
func Logger() *slog.Logger {
	return slog.Default()
}

// ---------- 内部实现 ----------

func parseLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
