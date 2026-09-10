// Package common `common/const.go` 必要说明:
// - 具体的业务内常量应在各自的业务模块中定义,
// - 此处主要用于共享的常量定义, 例如全局的日志级别, context key 等, 以便在整个项目中统一使用, 避免硬编码和重复定义.
// - 例如 auth 模块的 JWT 相关常量, 应在 auth 模块中维护独立的 const.go 文件, 而不是在这里 (common/const.go) 定义.
// 额外说明:
// - 需要根据实际业务情况变动的常量, 例如 JWT 的过期时间, 应在配置文件 CustomConfig 中定义, 而不是在代码中硬编码.
package constant

import "log/slog"

// 自定义日志级别
const (
	LevelDebug = slog.LevelDebug
	LevelInfo  = slog.LevelInfo
	LevelWarn  = slog.LevelWarn
	LevelError = slog.LevelError
)

const (
	// CtxKeyTraceID 用于在 context 中存储 trace_id
	CtxKeyTraceID string = "trace_id"
	// CtxKeyUserID 用于在 context 中存储用户 ID
	CtxKeyUserID string = "user_id"
	// CtxKeyClientIP 用于在 context 中存储客户端 IP
	CtxKeyClientIP string = "client_ip"
)

const (
	// BcryptCost bcrypt 加密成本，越高越安全，但越慢
	BcryptCost = 12 // bcrypt 加密成本，越高越安全，但越慢
)
