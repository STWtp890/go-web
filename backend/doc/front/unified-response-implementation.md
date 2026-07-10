# 后端统一响应实现原理

> **日期**: 2026-07-05  
> **版本**: v2.0  
> **读者**: 前端 / 后端开发

---

## 一、问题背景

### 改造前

```
Logic 返回 (resp, nil)  with resp.Code = 403
        ↓
Handler: err == nil → httpx.OkJsonCtx(w, resp)
        ↓
HTTP 200  {"code":403, "message":"Permission denied"}
        ↓
axios: response.status === 200 → 走 success 回调
       catch 永远不到达，refresh token 机制失效
```

### 改造后

```
Logic 返回 (nil, &BizError{Code:403, HTTPStatus:403})
        ↓
Handler: err != nil → httpx.ErrorCtx(r.Context(), w, err)
        ↓
Global ErrorHandler: errors.As(err, &bizErr) → (403, bizErr)
        ↓
HTTP 403  {"code":403, "message":"forbidden: ..."}
        ↓
axios: response.status === 403 → 走 error 回调 → catch 可到达
```

---

## 二、核心组件

### 2.1 `BizError` — 错误载体

```go
// common/bizerror.go
type BizError struct {
    Code       int    `json:"code"`       // 业务错误码
    Message    string `json:"message"`    // 错误消息（不暴露内部细节）
    HTTPStatus int    `json:"-"`          // HTTP 状态码（不序列化到 JSON）
}

func NewBizError(code int, message string) *BizError {
    return &BizError{Code: code, Message: message, HTTPStatus: code}
}
```

### 2.2 `SetErrorHandlerCtx` — 全局错误处理器

```go
// api.go — main()
httpx.SetErrorHandlerCtx(func(ctx context.Context, err error) (int, any) {
    var bizErr *BizError
    if errors.As(err, &bizErr) {          // 类型断言 + 解包装
        return bizErr.HTTPStatus, bizErr  // (HTTP状态码, JSON body)
    }
    return 500, map[string]any{           // 未知错误 → 500
        "code": 500, "message": err.Error(),
    }
})
```

**关键设计**：`bizErr` 是 `*BizError` 指针。`doHandleError` 中 `switch v := body.(type)` 先匹配 `case error:`。`*BizError` 实现了 `Error()` 方法，但作为**指针类型**传入时不会被 `case error` 捕获（go-zero 对该分支做了具体类型判断），从而正确走 `default` → `writeJson` 输出 JSON。

---

## 三、完整数据流

```
┌─ Logic 层 ─────────────────────────────────────────────────────────┐
│                                                                     │
│  func (l *SomeLogic) SomeMethod(req) (resp, err) {                 │
│      if err := common.RequirePermission(l.ctx, ...); err != nil {  │
│          return nil, err   // ← err 是 *BizError                   │
│      }                                                              │
│      // ...                                                         │
│  }                                                                  │
│                                                                     │
│  → 错误从 rbac.go 逐层向上传递：                                     │
│    checkRolePermission() → checkUserRole() → checkUser()            │
│         ↓                     ↓                 ↓                  │
│    NewBizError(403,      NewBizError(403,   NewBizError(401,       │
│      "role lacks          "role mismatch")   "invalid token")       │
│      permission")                                                   │
└──────────────────────┬──────────────────────────────────────────────┘
                       │ return nil, err
                       ▼
┌─ Handler 层（自动生成，不可改）─────────────────────────────────────┐
│                                                                     │
│  resp, err := l.SomeLogic(&req)                                     │
│  if err != nil {                                                    │
│      httpx.ErrorCtx(r.Context(), w, err)                            │
│  }                                                                  │
│                                                                     │
│  → buildErrorHandler(r.Context())                                   │
│       ↓                                                             │
│    handlerCtx(ctx, err)   ← ctx == r.Context()                      │
└──────────────────────┬──────────────────────────────────────────────┘
                       │
                       ▼
┌─ Global ErrorHandler ──────────────────────────────────────────────┐
│                                                                     │
│  func(ctx context.Context, err error) (int, any) {                  │
│      var bizErr *BizError                                           │
│      if errors.As(err, &bizErr) {                                   │
│          return bizErr.HTTPStatus, bizErr                           │
│      }                                                              │
│      return 500, {"code":500, "message": err.Error()}                │
│  }                                                                  │
│                                                                     │
│  → doHandleError:                                                    │
│    writeJson(w, code, body)                                         │
│    → Content-Type: application/json                                 │
│    → HTTP Status: code                                              │
│    → Body: {"code":403, "message":"forbidden: role mismatch"}       │
└─────────────────────────────────────────────────────────────────────┘
```

---

## 四、错误码速查

| 层级 | 场景 | HTTP 状态码 | Body.code | Body.message |
|---|---|---|---|---|
| JWT 中间件 | 无 Token / Token 过期 | 401 | — | go-zero 默认 |
| `checkUser` | Token 中 userId 无法解析 | 401 | 401 | `unauthorized: invalid token` |
| `checkUser` | users 表查不到该用户 | 401 | 401 | `unauthorized: user not found` |
| `checkUser` | 用户已软删除 | 401 | 401 | `unauthorized: user deleted` |
| `checkUserRole` | user_roles 无记录 | 403 | 403 | `forbidden: no role assigned` |
| `checkUserRole` | Token roleId ≠ DB roleId | 403 | 403 | `forbidden: role mismatch` |
| `checkRolePermission` | permissions 表无该 resource:action | 403 | 403 | `forbidden: permission not defined` |
| `checkRolePermission` | role_permissions 无绑定 | 403 | 403 | `forbidden: role lacks permission` |
| `requireAuthor` | 非作者本人操作 | 403 | 403 | `forbidden: not the author` |
| 未知错误 | panic / 未分类错误 | 500 | 500 | `err.Error()` |

---

## 五、前端适配指南

### 5.1 axios 拦截器（已实现）

```js
// src/api/index.js
api.interceptors.response.use(
  (response) => {
    const data = response.data
    // 兜底：即使 HTTP 200，若 body.code ≠ 200 也 reject
    if (data && typeof data.code === 'number' && data.code !== 200) {
      const err = new Error(data.message || 'Request failed')
      err.response = response
      err.code = data.code
      return Promise.reject(err)
    }
    return data
  },
  async (error) => {
    // HTTP 401 → 自动 refresh token
    if (error.response?.status === 401 && !originalRequest._retry) {
      // ... refresh 逻辑
    }
    return Promise.reject(error)
  }
)
```

### 5.2 业务代码使用

```js
// 方式 1：try/catch
try {
  const data = await markdownAPI.upload({ title, content })
  // data.code === 200，正常处理
} catch (e) {
  // e.code === 403 | 401 | 500
  notify.error(e.response?.data?.message || '操作失败')
}

// 方式 2：.then/.catch
markdownAPI.delete(id)
  .then(data => notify.success('删除成功'))
  .catch(e => notify.error(e.response?.data?.message || '删除失败'))
```

### 5.3 错误处理最佳实践

```js
function handleApiError(e, fallbackMsg = '操作失败') {
  const msg = e?.response?.data?.message || e?.message || fallbackMsg
  const code = e?.response?.data?.code || e?.code || 0

  switch (code) {
    case 401:
      // Token 失效，跳转登录
      authStore.logout()
      router.push('/login')
      break
    case 403:
      notify.warning(msg)  // 权限不足，不跳转
      break
    default:
      notify.error(msg)
  }
}
```

---

## 六、新增模块适配清单

在新 logic 模块中添加权限检查时，按以下步骤操作：

### 6.1 定义模块错误（如需要）

```go
// logic/yourmodule/err.go
var (
    ErrModuleNotFound = common.NewBizError(404, "module: resource not found")
    ErrModuleDenied   = common.NewBizError(403, "module: access denied")
)
```

### 6.2 Logic 函数中返回

```go
func (l *YourLogic) YourMethod(req) (resp, err) {
    if err := common.RequirePermission(l.ctx, l.svcCtx.CustomCtx.SQLModel,
        "resource", "action"); err != nil {
        return nil, err  // ← 直接向上传递 BizError
    }
    // 自定义业务错误
    if somethingNotFound {
        return nil, ErrModuleNotFound  // ← HTTP 404
    }
    // ...
}
```

### 6.3 关键原则

```
✅ return nil, err      → err 流经 handler → ErrorCtx → 全局 handler → 正确 HTTP 状态码
❌ return resp, nil     → err == nil → OkJsonCtx → 永远 HTTP 200
```

---

## 七、架构优势

| 维度 | 改造前 | 改造后 |
|---|---|---|
| HTTP 语义 | 所有响应 200，状态码无效 | 401/403/404/500 各归其位 |
| 前端错误处理 | 必须手动检查 `body.code` | `catch` 自动触发 |
| Token 刷新 | 无法触发（refresh 依赖 HTTP 401） | HTTP 401 → 自动 refresh |
| 调试体验 | curl 看 200 以为成功 | curl 看到真实状态码 |
| 网关/负载均衡 | 无法按状态码做监控统计 | 标准 HTTP 监控可用 |
| 扩展性 | 每个模块重复 `BaseResp{Code:...}` | `NewBizError(code, msg)` 一行搞定 |
