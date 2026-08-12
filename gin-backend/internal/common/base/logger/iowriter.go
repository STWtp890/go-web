package logger

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"gin-backend/internal/config/must"
)

// GinWriter 返回 Gin 内部 debug 日志用的 io.Writer
// gin 自身会打印 "[GIN-debug] Listening and serving..." 等信息
func GinWriter(cfg must.LogConfig) io.Writer {
    return buildWriter(cfg)
}


func buildWriter(cfg must.LogConfig) io.Writer {
    if cfg.Output == "stdout" || cfg.FilePath == "" {
        return os.Stdout
    }

    dir := filepath.Dir(cfg.FilePath)
    if err := os.MkdirAll(dir, 0o755); err != nil {
        fmt.Fprintf(os.Stderr, "logger: create log dir failed: %v\n", err)
        return os.Stdout
    }

    f, err := os.OpenFile(cfg.FilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
    if err != nil {
        fmt.Fprintf(os.Stderr, "logger: open log file failed: %v\n", err)
        return os.Stdout
    }
    return f
}