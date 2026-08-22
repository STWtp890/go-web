# HttpOnly Cookie JWT 认证与实时连接

## 背景

本项目的 HTTP API 和聊天 WebSocket 连接需要基于 JWT 识别用户身份，并通过 JWT 中的 `sid` 与 Redis 当前会话比对，实现登出、异地登录覆盖和强制下线后的即时失效。AI Agent 的 SSE 属于服务间出站接入，不使用浏览器 Cookie。

原始前端方案把 access / refresh JWT 保存在浏览器 `localStorage`，普通 HTTP 请求通过：

```http
Authorization: Bearer <access-token>
```

完成鉴权。这个模式对 `fetch` 有效，但无法直接适用于浏览器原生实时连接。

## 历史问题（已解决）

聊天模块的实时入口为：

```text
GET /api/v1/protected/chat/ws
```

后端将它放在 `AuthRequired` 保护路由组下。因此 WebSocket 协议升级之前，HTTP 握手必须先通过 JWT 鉴权。

但浏览器原生 API 存在限制：

```ts
new WebSocket(url)
```

它不能由页面 JavaScript 添加任意 HTTP Header，特别是不能设置 `Authorization`。这会造成以下结果：

1. 前端虽然拥有 localStorage 中的 access token；
2. `fetch` 调用聊天 HTTP 接口仍能成功；
3. 原生 WebSocket 握手却没有 Authorization；
4. 后端中间件返回 `401`，协议无法升级或事件流无法建立；
5. 聊天页只能展示 HTTP 发送状态，无法安全接收实时消息。

浏览器不允许随意自定义握手 Header 是平台约束，不是 Gin、Gorilla WebSocket 或 Vue 的缺陷。服务端 SDK、Node.js 客户端可以设置 Header，不代表浏览器页面也可以。

## 原理

WebSocket 的第一步确实是 HTTP Upgrade 请求，但随后会升级为 WebSocket 帧协议。浏览器为了安全和互操作性，只暴露 URL 与少数固定选项，不允许网页自行构造 Upgrade 请求头。

Cookie 是浏览器在 WebSocket 握手中被允许自动附带的认证载体：

```text
页面登录
  → 后端 Set-Cookie(HttpOnly access JWT)
  → 浏览器保存 Cookie
  → fetch / WebSocket Upgrade 自动携带路径匹配的 Cookie
  → Gin 中间件读取 Cookie、验签 JWT、校验 Redis sid
  → 建立安全连接
```

`HttpOnly` 的含义是页面 JavaScript 不能读取 Cookie 值。即使出现常见的 XSS 脚本注入，脚本也不能直接窃取 JWT；它不表示 Cookie 不会自动随请求发送，因此仍需要 CSRF 与 Origin 防护。

## 方案选择

可选路径及结论：

| 方案 | 优点 | 局限 | 结论 |
|---|---|---|---|
| 一次性连接 ticket | 可用于 WebSocket URL | SSE 自动重连与一次性 ticket 不自然匹配 | 可用于特殊场景 |
| JWT 放 query 参数 | 接入简单 | 容易出现在日志、历史记录、监控与代理中 | 不采用 |
| HttpOnly Cookie | 原生 WebSocket 支持，JWT 不可被 JS 读取 | 需要处理 CSRF、CORS、Origin 与 HTTPS | 项目采用 |

## 当前项目实现

### 会话 Cookie

后端在登录和刷新成功后写入以下 Cookie：

| 会话 | Cookie | 作用域 | JavaScript 可读 |
|---|---|---|---|
| 用户 access | `pp_user_at` | `Path=/api/v1/protected`，HttpOnly | 否 |
| 用户 refresh | `pp_user_rt` | `Path=/api/v1/public/auth/refresh`，HttpOnly | 否 |
| 用户 CSRF | `pp_user_csrf` | `Path=/`，非 HttpOnly | 是 |
| 管理员 access | `pp_manager_at` | `Path=/api/v1/protected/manager`，HttpOnly | 否 |
| 管理员 refresh | `pp_manager_rt` | `Path=/api/v1/public/manager/refresh`，HttpOnly | 否 |
| 管理员 CSRF | `pp_manager_csrf` | `Path=/`，非 HttpOnly | 是 |

access 与 refresh Cookie 使用最小路径范围，避免把 refresh JWT 发送到不相关的接口。CSRF Cookie 不是身份凭据，必须放在 `/`，供 SPA 页面读取并写入请求 Header。

### 服务端认证顺序

认证中间件仅按以下顺序从 Cookie 取得 access JWT：

1. 用户接口读取 `pp_user_at`，管理员接口读取 `pp_manager_at`；
2. 固定使用 RS256 验签，检查 `token_use=access`；
3. 从 claims 读取 `sub`、`sid`；
4. 查询 Redis，确认 `sid` 仍是主体当前有效会话；
5. 将 claims 注入 Gin Context，允许后续 HTTP 与 WebSocket Handler 执行。

后端不接受 `Authorization: Bearer`，登录和刷新响应也不返回 JWT；浏览器只能通过 HttpOnly Cookie 发送会话凭据。

### 实时连接保护

浏览器访问聊天连接入口时，路径匹配的 `pp_user_at` 会自动发送：

```ts
const socket = new WebSocket('ws://localhost:15173/api/v1/protected/chat/ws')
```

服务器仅在 Cookie JWT 和 Redis `sid` 都有效时升级 WebSocket。握手会验证浏览器发来的 `Origin` 是否属于后端 CORS 白名单，拒绝未授权站点创建携带 Cookie 的连接。

若用户在其他设备登录、主动登出或被撤销，Redis 中的当前 `sid` 会改变或删除。已建立连接会由既有会话撤销机制关闭；旧连接后续重连及旧 HTTP 请求也会失败。

### CSRF 防护

Cookie 会由浏览器隐式附带，故不能只依赖“请求带有 JWT”判断写操作安全。

项目对 Cookie 鉴权的 `POST`、`PUT`、`PATCH`、`DELETE` 强制要求：

```http
X-CSRF-Token: <对应 pp_*_csrf 的值>
```

后端检查 Header 与 Cookie 完全一致，并验证该值的签名与当前 JWT `sid` 绑定。该机制防止攻击站点伪造可通过的双提交 Token。Cookie refresh 也需要 CSRF 校验，因为它会旋转会话凭据。

安全方法（`GET`、`HEAD`、`OPTIONS`）以及 WebSocket 建连不要求 CSRF Header；它们仍受 Cookie JWT、Redis sid 和 Origin 限制。

## 前端改造要求

前端应将请求客户端统一调整为：

```ts
fetch(url, {
  credentials: 'include',
  headers: isUnsafeMethod
    ? { 'X-CSRF-Token': readCSRFCookie(scope) }
    : undefined,
})
```

并完成以下事项：

1. 删除 localStorage / sessionStorage 中的 access、refresh JWT；
2. 不再主动写入 Authorization Header；
3. 保留按用户、管理员 scope 隔离的刷新单飞逻辑，但 refresh 不再接收 token 参数；
4. 用受保护的 session 查询接口初始化内存态与路由守卫，而不是解码 JWT；
5. 用原生 WebSocket 建立双向聊天；
6. 所有写接口、投递确认和登出都自动携带对应 CSRF Header；
7. 在 `401` 时单飞刷新一次，刷新失败则清理前端展示状态、关闭实时连接并跳转登录。

面向 Vue 前端的具体文件级改造、示例代码与验收步骤见 [simple-frontend/httpOnlyCookieMigration.md](./simple-frontend/httpOnlyCookieMigration.md)。

## 部署要求

开发配置使用：

```yaml
custom:
  cookie:
    secure: false
    domain: ""
    same_site: lax
```

生产环境必须满足：

1. 全站 HTTPS / WSS，并将 `secure` 设为 `true`；
2. 保持 Cookie `domain` 为空以使用 host-only Cookie，除非有明确的同站点子域需求；
3. 使用部署环境独立的高熵 `csrf_secret`，不得沿用示例值；
4. CORS 只列出精确的前端 Origin，且启用 `AllowCredentials` 时绝不能使用 `*`；
5. 如果前端与 API 真正跨站，使用 `SameSite=None; Secure`，并重新评估浏览器第三方 Cookie 策略与 CSRF 边界。

## 当前进度与验收

已完成：

1. 后端 Cookie、CSRF、CORS、Origin 支持；
2. 前端 HTTP 客户端与会话 Store 切换 Cookie；
3. 聊天页使用原生 WebSocket，并支持会话校验后的退避重连；
4. 删除 Bearer 兼容、Token 响应字段与前端 Token Vault。

待在目标部署环境完成联调验证：登录、刷新、登出、异地登录覆盖、CSRF 拒绝、跨标签页状态同步与长连接断线重连。

最终验收标准：浏览器 DevTools 的 localStorage、sessionStorage、前端源码和连接 URL 中不再出现 access / refresh JWT；已登录用户可通过原生 WebSocket 安全建立实时连接；跨站连接和缺失 CSRF 的写操作被拒绝。
