// Package identity 定义请求级可信身份契约。
//
// 鉴权中间件在完成 token 验签与会话校验后, 把解析结果收敛为 Principal 注入请求上下文;
// 业务层只读取 Principal, 不再接触 JWT claims, 也不再自行解析 token。
//
// 本包属于 HTTP 传输层横切关注点, 位于 platform/httpserver 之下, 不承载业务规则。
package identity

import (
	"context"

	"github.com/gin-gonic/gin"
)

// Kind 标识身份所属的账号域。
type Kind string

const (
	// KindUser 前台用户 (users 表)。
	KindUser Kind = "user"
	// KindManager 管理员 (managers 表)。
	KindManager Kind = "manager"
)

// Principal 是中间件解析并校验后的可信身份。
//
// 只有 platform/httpserver/middleware 会构造并注入该类型; 业务层无法自行伪造。
type Principal struct {
	// Kind 账号域 (users / managers)。
	Kind Kind
	// Subject 是 JWT sub 原文 (users.id / managers.id 的字符串形式)。
	Subject string
	// UserID 是 Subject 的规范化数值形式; 无法解析或非正数时为 0。
	UserID int64
	// SessionID 是 JWT sid, 用于会话级校验 (CSRF、强制下线、会话事件)。
	SessionID string
}

// ctxKey 是标准 context 使用的私有键类型, 避免跨包字符串键冲突。
type ctxKey struct{}

// ginContextKey 是 Gin 请求上下文使用的键。
//
// gin.Context 的 Set/Get 只接受 string 键, 无法使用私有类型; 因此把该字符串
// 收敛为包内未导出常量, 使裸字符串键不再出现在业务代码中。
const ginContextKey = "identity.Principal"

// With 把身份写入标准 context, 供非 Gin 层 (审计、日志、下游调用) 传递。
func With(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, principal)
}

// From 从标准 context 读取身份。
func From(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(ctxKey{}).(Principal)
	return principal, ok
}

// Set 由鉴权中间件调用, 把身份同时注入 Gin 请求上下文与底层标准 context。
func Set(c *gin.Context, principal Principal) {
	c.Set(ginContextKey, principal)
	if c.Request != nil {
		c.Request = c.Request.WithContext(With(c.Request.Context(), principal))
	}
}

// FromGin 读取当前请求身份; 请求未经鉴权中间件时返回 false。
func FromGin(c *gin.Context) (Principal, bool) {
	value, exists := c.Get(ginContextKey)
	if !exists {
		return Principal{}, false
	}
	principal, ok := value.(Principal)
	if !ok {
		return Principal{}, false
	}
	return principal, true
}

// UserID 返回当前请求的数值主体标识; 未认证或主体非法时返回 false。
func UserID(c *gin.Context) (int64, bool) {
	principal, ok := FromGin(c)
	if !ok || principal.UserID <= 0 {
		return 0, false
	}
	return principal.UserID, true
}

// SessionID 返回当前请求的会话标识; 未认证或缺少 sid 时返回 false。
func SessionID(c *gin.Context) (string, bool) {
	principal, ok := FromGin(c)
	if !ok || principal.SessionID == "" {
		return "", false
	}
	return principal.SessionID, true
}
